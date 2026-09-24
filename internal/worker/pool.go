// Package worker provides a bounded worker pool with a bounded in-memory queue.
package worker

import (
	"context"
	"log/slog"
	"runtime/debug"
	"sync"
)

// Job is a unit of work. ctx is cancelled when shutdown's grace period expires.
type Job func(ctx context.Context)

// Pool runs jobs on a fixed number of goroutines fed by a bounded queue.
type Pool struct {
	jobs   chan Job
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	logger *slog.Logger

	mu     sync.RWMutex
	closed bool
}

// New starts workers goroutines consuming from a queue holding at most queueSize pending jobs.
func New(workers, queueSize int, logger *slog.Logger) *Pool {
	ctx, cancel := context.WithCancel(context.Background())
	p := &Pool{
		jobs:   make(chan Job, queueSize),
		ctx:    ctx,
		cancel: cancel,
		logger: logger,
	}
	for range workers {
		p.wg.Go(p.run)
	}
	return p
}

func (p *Pool) run() {
	for job := range p.jobs {
		p.safeRun(job)
	}
}

// safeRun keeps one misbehaving job from taking down the whole process.
func (p *Pool) safeRun(job Job) {
	defer func() {
		if r := recover(); r != nil {
			p.logger.Error("worker job panicked", slog.Any("panic", r), slog.String("stack", string(debug.Stack())))
		}
	}()
	job(p.ctx)
}

// TrySubmit enqueues job without blocking. It returns false if the queue is full or the pool is shut down.
func (p *Pool) TrySubmit(job Job) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.closed {
		return false
	}
	select {
	case p.jobs <- job:
		return true
	default:
		return false
	}
}

// Shutdown stops accepting jobs and waits for queued and running jobs to finish.
// If ctx expires first, running jobs are cancelled and ctx.Err() is returned.
func (p *Pool) Shutdown(ctx context.Context) error {
	p.mu.Lock()
	if !p.closed {
		p.closed = true
		close(p.jobs)
	}
	p.mu.Unlock()

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		p.cancel()
		return nil
	case <-ctx.Done():
		p.cancel()
		<-done
		return ctx.Err()
	}
}
