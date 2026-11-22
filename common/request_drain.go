package common

import (
	"context"
	"sync"
)

// ShutdownRequests tracks work that http.Server.Shutdown cannot wait for, such
// as hijacked WebSocket sessions. It is drained once, when the process exits.
var ShutdownRequests = NewRequestDrain()

// RequestDrain is a one-way admission gate and completion barrier. Its zero
// value is ready for use. Do not copy it after first use.
type RequestDrain struct {
	mu       sync.Mutex
	stopping chan struct{}
	drained  chan struct{}
	active   int
	draining bool
}

func NewRequestDrain() *RequestDrain {
	return &RequestDrain{}
}

// Register atomically admits work unless draining has begun. Register a session
// before upgrading, and each response.create before starting its runner so new
// work cannot slip through while a session's shutdown watcher is waking up.
//
// On success, stopping is closed when admission stops. Its receiver must cancel
// work and unblock network reads without waiting inside Register/BeginDrain.
// Call the idempotent done only after ALL settlement and cleanup have returned;
// closing a socket or canceling a context alone is not completion. If ok is
// false, stopping is already closed and done is a harmless no-op.
func (d *RequestDrain) Register() (stopping <-chan struct{}, done func(), ok bool) {
	d.mu.Lock()
	d.initLocked()
	if d.draining {
		d.mu.Unlock()
		return d.stopping, func() {}, false
	}
	d.active++
	stopping = d.stopping
	d.mu.Unlock()

	var once sync.Once
	done = func() {
		once.Do(func() {
			d.mu.Lock()
			defer d.mu.Unlock()
			d.active--
			if d.draining && d.active == 0 {
				close(d.drained)
			}
		})
	}
	return stopping, done, true
}

// BeginDrain rejects future registrations and broadcasts shutdown. It never
// calls business code or waits for a session while holding the registry lock.
func (d *RequestDrain) BeginDrain() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.initLocked()
	if d.draining {
		return
	}
	d.draining = true
	close(d.stopping)
	if d.active == 0 {
		close(d.drained)
	}
}

// Wait begins draining if needed, then waits within the caller's existing
// shutdown budget. Cancellation does not discard registrations: a later Wait
// can still observe their eventual completion. No waiter goroutine is created.
func (d *RequestDrain) Wait(ctx context.Context) error {
	d.BeginDrain()
	d.mu.Lock()
	drained := d.drained
	d.mu.Unlock()

	// A fully drained registry needs no remaining budget, even after HTTP used
	// it all. The caller must still pass the same context to its final flush.
	select {
	case <-drained:
		return nil
	default:
	}
	select {
	case <-drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (d *RequestDrain) initLocked() {
	if d.stopping == nil {
		d.stopping = make(chan struct{})
		d.drained = make(chan struct{})
	}
}
