package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// fakeSettler records which transfer IDs were executed.
type fakeSettler struct {
	mu  sync.Mutex
	ids []int64
	err error
}

func (f *fakeSettler) Execute(ctx context.Context, transferID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ids = append(f.ids, transferID)
	return f.err
}

func (f *fakeSettler) executed() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int64(nil), f.ids...)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestWorkerProcessesEnqueuedJobs(t *testing.T) {
	settler := &fakeSettler{}
	w := New(settler, discardLogger())

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.Run()
	}()

	ctx := context.Background()
	for id := int64(1); id <= 5; id++ {
		if err := w.Enqueue(ctx, id); err != nil {
			t.Fatalf("Enqueue(%d): %v", id, err)
		}
	}

	// Stop closes the queue and blocks until every accepted job is drained.
	w.Stop()
	wg.Wait()

	got := settler.executed()
	if len(got) != 5 {
		t.Fatalf("executed %d jobs, want 5: %v", len(got), got)
	}
	for i, id := range got {
		if want := int64(i + 1); id != want {
			t.Errorf("job %d: executed transfer %d, want %d (FIFO order)", i, id, want)
		}
	}
}

func TestEnqueueReturnsErrorWhenContextCancelled(t *testing.T) {
	settler := &fakeSettler{}
	w := New(settler, discardLogger())
	// No Run() goroutine: fill the buffered queue so Enqueue must block.
	ctx := context.Background()
	for i := 0; i < cap(w.jobs); i++ {
		if err := w.Enqueue(ctx, int64(i)); err != nil {
			t.Fatalf("filling queue: %v", err)
		}
	}

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	err := w.Enqueue(cancelledCtx, 999)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Enqueue on full queue with cancelled context: got %v, want context.Canceled", err)
	}
}

func TestWorkerContinuesAfterSettlementFailure(t *testing.T) {
	settler := &fakeSettler{err: errors.New("boom")}
	w := New(settler, discardLogger())

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.Run()
	}()

	ctx := context.Background()
	for id := int64(1); id <= 3; id++ {
		if err := w.Enqueue(ctx, id); err != nil {
			t.Fatalf("Enqueue(%d): %v", id, err)
		}
	}
	w.Stop()
	wg.Wait()

	// A failed settlement is logged, not fatal: later jobs still run.
	if got := len(settler.executed()); got != 3 {
		t.Fatalf("executed %d jobs, want 3 despite errors", got)
	}
}

func TestStopDrainsWithoutLosingJobs(t *testing.T) {
	settler := &fakeSettler{}
	w := New(settler, discardLogger())

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.Run()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	const n = 20
	for id := int64(1); id <= n; id++ {
		if err := w.Enqueue(ctx, id); err != nil {
			t.Fatalf("Enqueue(%d): %v", id, err)
		}
	}
	w.Stop()
	wg.Wait()

	if got := len(settler.executed()); got != n {
		t.Fatalf("drained %d jobs, want %d", got, n)
	}
}
