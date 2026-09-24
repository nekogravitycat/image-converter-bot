package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestQueueIsBounded(t *testing.T) {
	block := make(chan struct{})
	p := New(1, 2, discard)
	defer func() {
		close(block)
		p.Shutdown(context.Background())
	}()

	started := make(chan struct{})
	if !p.TrySubmit(func(context.Context) { close(started); <-block }) {
		t.Fatal("first submit rejected")
	}
	<-started // the only worker is now busy

	for i := range 2 {
		if !p.TrySubmit(func(context.Context) {}) {
			t.Fatalf("submit %d rejected while queue has room", i)
		}
	}
	if p.TrySubmit(func(context.Context) {}) {
		t.Fatal("submit accepted with a full queue")
	}
}

func TestConcurrencyIsBounded(t *testing.T) {
	const workers = 2
	p := New(workers, 16, discard)
	var running, peak atomic.Int32
	for range 10 {
		p.TrySubmit(func(context.Context) {
			n := running.Add(1)
			for {
				old := peak.Load()
				if n <= old || peak.CompareAndSwap(old, n) {
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
			running.Add(-1)
		})
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if peak.Load() > workers {
		t.Fatalf("peak concurrency %d exceeds %d workers", peak.Load(), workers)
	}
}

func TestShutdownDrainsAndRejects(t *testing.T) {
	p := New(1, 8, discard)
	var ran atomic.Int32
	for range 5 {
		p.TrySubmit(func(context.Context) { ran.Add(1) })
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ran.Load() != 5 {
		t.Fatalf("ran %d jobs, want 5", ran.Load())
	}
	if p.TrySubmit(func(context.Context) {}) {
		t.Fatal("submit accepted after shutdown")
	}
}

func TestShutdownTimeoutCancelsJobs(t *testing.T) {
	p := New(1, 1, discard)
	started := make(chan struct{})
	p.TrySubmit(func(ctx context.Context) {
		close(started)
		<-ctx.Done()
	})
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := p.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
}

func TestPanicDoesNotKillWorker(t *testing.T) {
	p := New(1, 4, discard)
	p.TrySubmit(func(context.Context) { panic("boom") })
	var ran atomic.Bool
	p.TrySubmit(func(context.Context) { ran.Store(true) })
	p.Shutdown(context.Background())
	if !ran.Load() {
		t.Fatal("job after panic did not run")
	}
}
