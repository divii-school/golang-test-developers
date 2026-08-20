package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"bank-api/internal/storage"
	"bank-api/internal/worker"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("server exited", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("DATABASE_URL env var is required")
	}

	db, err := storage.Open(dsn)
	if err != nil {
		return fmt.Errorf("connect to db: %w", err)
	}
	defer db.Close()
	logger.Info("connected to database")

	accounts := &storage.AccountStore{DB: db}
	transfers := &storage.TransferStore{DB: db}

	// ctx is cancelled on SIGINT/SIGTERM; it drives graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	w := worker.New(transfers, logger)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.Run()
	}()

	// Requeue transfers accepted before a previous shutdown or crash.
	pending, err := transfers.ListPendingIDs(ctx)
	if err != nil {
		return fmt.Errorf("list pending transfers: %w", err)
	}
	for _, id := range pending {
		if err := w.Enqueue(ctx, id); err != nil {
			return fmt.Errorf("requeue transfer %d: %w", id, err)
		}
	}
	if len(pending) > 0 {
		logger.Info("requeued pending transfers", "count", len(pending))
	}

	srv := &http.Server{
		Addr:         ":8000",
		Handler:      newMux(logger, accounts, transfers, w),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		<-ctx.Done()
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("server shutdown", "error", err)
		}
	}()

	logger.Info("bank-api listening", "addr", srv.Addr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	// The server has stopped accepting requests; drain jobs already queued.
	w.Stop()
	wg.Wait()
	logger.Info("worker drained, bye")
	return nil
}

func newMux(logger *slog.Logger, accounts *storage.AccountStore, transfers *storage.TransferStore, w *worker.Worker) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(rw http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(rw, "ok")
	})

	mux.HandleFunc("POST /accounts", func(rw http.ResponseWriter, r *http.Request) {
		var req struct {
			Owner    string `json:"owner"`
			Currency string `json:"currency"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(rw, "invalid JSON", http.StatusBadRequest)
			return
		}
		if req.Owner == "" || req.Currency == "" {
			http.Error(rw, "owner and currency are required", http.StatusBadRequest)
			return
		}

		a, err := accounts.Create(r.Context(), req.Owner, req.Currency)
		if err != nil {
			logger.Error("create account", "error", err)
			http.Error(rw, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(rw, http.StatusCreated, a)
	})

	mux.HandleFunc("GET /accounts", func(rw http.ResponseWriter, r *http.Request) {
		list, err := accounts.List(r.Context())
		if err != nil {
			logger.Error("list accounts", "error", err)
			http.Error(rw, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(rw, http.StatusOK, list)
	})

	mux.HandleFunc("GET /accounts/{id}", func(rw http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(rw, "invalid account id", http.StatusBadRequest)
			return
		}
		a, err := accounts.GetByID(r.Context(), id)
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(rw, "account not found", http.StatusNotFound)
			return
		}
		if err != nil {
			logger.Error("get account", "id", id, "error", err)
			http.Error(rw, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(rw, http.StatusOK, a)
	})

	mux.HandleFunc("POST /transfers", func(rw http.ResponseWriter, r *http.Request) {
		var req struct {
			FromAccountID int64 `json:"from_account_id"`
			ToAccountID   int64 `json:"to_account_id"`
			Amount        int64 `json:"amount"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(rw, "invalid JSON", http.StatusBadRequest)
			return
		}
		if req.Amount <= 0 {
			http.Error(rw, "amount must be positive", http.StatusBadRequest)
			return
		}
		if req.FromAccountID == req.ToAccountID {
			http.Error(rw, "cannot transfer to the same account", http.StatusBadRequest)
			return
		}

		t, err := transfers.Create(r.Context(), req.FromAccountID, req.ToAccountID, req.Amount)
		if err != nil {
			// Most likely a foreign-key violation: unknown account id.
			logger.Error("create transfer", "error", err)
			http.Error(rw, "could not create transfer (do both accounts exist?)", http.StatusBadRequest)
			return
		}

		if err := w.Enqueue(r.Context(), t.ID); err != nil {
			// Client gave up while the queue was full; the transfer stays
			// pending and will be requeued on next startup.
			logger.Warn("enqueue transfer", "transfer_id", t.ID, "error", err)
			http.Error(rw, "transfer accepted but queue is busy", http.StatusServiceUnavailable)
			return
		}

		// 202: accepted for background settlement, not settled yet.
		writeJSON(rw, http.StatusAccepted, t)
	})

	mux.HandleFunc("GET /transfers/{id}", func(rw http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(rw, "invalid transfer id", http.StatusBadRequest)
			return
		}
		t, err := transfers.GetByID(r.Context(), id)
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(rw, "transfer not found", http.StatusNotFound)
			return
		}
		if err != nil {
			logger.Error("get transfer", "id", id, "error", err)
			http.Error(rw, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(rw, http.StatusOK, t)
	})

	return mux
}

func writeJSON(rw http.ResponseWriter, status int, v any) {
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(status)
	if err := json.NewEncoder(rw).Encode(v); err != nil {
		slog.Error("encode response", "error", err)
	}
}
