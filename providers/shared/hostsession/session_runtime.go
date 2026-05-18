package hostsession

import (
	"context"
	"sync"
)

type sessionRuntime struct {
	mu               sync.Mutex
	cond             *sync.Cond
	activeCalls      int
	resetInProgress  bool
	lastResetErr     error
	ensureInProgress bool
	lastEnsureErr    error
	hostReady        bool
	hostReadyRoot    bool
	readyInProgress  bool
	lastReadyErr     error
}

func newSessionRuntime() *sessionRuntime {
	runtime := &sessionRuntime{}
	runtime.cond = sync.NewCond(&runtime.mu)
	return runtime
}

func (r *sessionRuntime) acquireCall(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.waitLocked(ctx, func() bool { return !r.resetInProgress }); err != nil {
		return err
	}

	r.activeCalls++
	return nil
}

func (r *sessionRuntime) releaseCall() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.activeCalls > 0 {
		r.activeCalls--
	}
	if r.activeCalls == 0 {
		r.cond.Broadcast()
	}
}

func (r *sessionRuntime) reserveCall() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.activeCalls++
}

func (r *sessionRuntime) beginReset(ctx context.Context) (bool, error, func(error)) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.resetInProgress {
		if err := r.waitLocked(ctx, func() bool { return !r.resetInProgress }); err != nil {
			return false, err, nil
		}
		return false, r.lastResetErr, nil
	}

	r.resetInProgress = true
	r.lastResetErr = nil

	finish := func(err error) {
		r.mu.Lock()
		defer r.mu.Unlock()

		r.lastResetErr = err
		r.resetInProgress = false
		r.cond.Broadcast()
	}

	return true, nil, finish
}

func (r *sessionRuntime) beginReadinessCheck(ctx context.Context, needRoot bool) (bool, error, func(error, bool)) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for {
		if !r.readyInProgress {
			break
		}
		if err := r.waitLocked(ctx, func() bool { return !r.readyInProgress }); err != nil {
			return false, err, nil
		}
		if r.readinessSatisfiedLocked(needRoot) {
			return false, nil, nil
		}
		if r.lastReadyErr != nil {
			return false, r.lastReadyErr, nil
		}
	}

	if r.readinessSatisfiedLocked(needRoot) {
		return false, nil, nil
	}

	r.readyInProgress = true
	r.lastReadyErr = nil

	finish := func(err error, rootValidated bool) {
		r.mu.Lock()
		defer r.mu.Unlock()

		if err == nil {
			r.hostReady = true
			if rootValidated {
				r.hostReadyRoot = true
			}
			r.lastReadyErr = nil
		} else {
			r.lastReadyErr = err
		}
		r.readyInProgress = false
		r.cond.Broadcast()
	}

	return true, nil, finish
}

func (r *sessionRuntime) beginEnsure(ctx context.Context) (bool, error, func(error)) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.ensureInProgress {
		if err := r.waitLocked(ctx, func() bool { return !r.ensureInProgress }); err != nil {
			return false, err, nil
		}
		return false, r.lastEnsureErr, nil
	}

	r.ensureInProgress = true
	r.lastEnsureErr = nil

	finish := func(err error) {
		r.mu.Lock()
		defer r.mu.Unlock()

		r.lastEnsureErr = err
		r.ensureInProgress = false
		r.cond.Broadcast()
	}

	return true, nil, finish
}

func (r *sessionRuntime) clearReadiness() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.hostReady = false
	r.hostReadyRoot = false
	r.readyInProgress = false
	r.lastReadyErr = nil
	r.cond.Broadcast()
}

func (r *sessionRuntime) readinessSatisfiedLocked(needRoot bool) bool {
	if needRoot {
		return r.hostReadyRoot
	}
	return r.hostReady
}

func (r *sessionRuntime) waitForNoActiveCalls(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.waitLocked(ctx, func() bool { return r.activeCalls == 0 })
}

func (r *sessionRuntime) waitLocked(ctx context.Context, ready func() bool) error {
	done := make(chan struct{})
	defer close(done)

	go func() {
		select {
		case <-ctx.Done():
			r.mu.Lock()
			r.cond.Broadcast()
			r.mu.Unlock()
		case <-done:
		}
	}()

	for !ready() {
		if err := ctx.Err(); err != nil {
			return err
		}
		r.cond.Wait()
	}

	return nil
}
