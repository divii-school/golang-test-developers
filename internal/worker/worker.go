// Package worker runs a background goroutine that settles queued transfers.
// HTTP handlers accept a transfer, persist it as pending, and enqueue its ID
// on a channel; the worker consumes the channel and executes each transfer
// against the database, so the request path never waits on settlement.
package worker

import (
	"context"
	"log/slog"
	"time"
)

type Job struct {
	TransferID int64
}

// Settler executes a queued transfer. *storage.TransferStore satisfies it;
// tests substitute a fake so the worker can be exercised without a database.
type Settler interface {
	Execute(ctx context.Context, transferID int64) error
}

type Worker struct {
	store Settler
	log   *slog.Logger
	jobs  chan Job
	done  chan struct{}
}

func New(store Settler, log *slog.Logger) *Worker {
	return &Worker{
		store: store,
		log:   log,
		jobs:  make(chan Job, 64),
		done:  make(chan struct{}),
	}
}

// Enqueue submits a transfer for background processing. If the queue is full
// it blocks until there is room or the caller's context is cancelled — the
// transfer stays pending in the database either way, so a requeue at next
// startup will pick it up.
func (w *Worker) Enqueue(ctx context.Context, transferID int64) error {
	select {
	case w.jobs <- Job{TransferID: transferID}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Run consumes jobs until Stop closes the queue. It is meant to be run in
// its own goroutine.
func (w *Worker) Run() {
	defer close(w.done)
	for job := range w.jobs {
		w.process(job)
	}
}

// Stop closes the queue and blocks until the worker has drained the jobs
// already accepted. Call it only after the HTTP server has stopped, so no
// handler can Enqueue on a closed channel.
func (w *Worker) Stop() {
	close(w.jobs)
	<-w.done
}

func (w *Worker) process(job Job) {
	// The originating request is long gone, so the job gets its own
	// deadline rather than inheriting a request context.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	if err := w.store.Execute(ctx, job.TransferID); err != nil {
		w.log.Error("transfer settlement failed",
			"transfer_id", job.TransferID, "error", err)
		return
	}
	w.log.Info("transfer settled",
		"transfer_id", job.TransferID, "took", time.Since(start))
}
