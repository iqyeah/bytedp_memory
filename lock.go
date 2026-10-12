package bytedp_memory

import (
	"context"
	"errors"
	"log"
	"time"

	lock "github.com/viney-shih/go-lock"
)

// Common lock timeout errors
var (
	ErrLockTimeout  = errors.New("lock acquisition timeout")
	ErrRLockTimeout = errors.New("rlock acquisition timeout")
)

// DefaultLockTimeout is the default timeout for lock acquisition
const DefaultLockTimeout = 1 * time.Second

// TimeoutRWMutex wraps go-lock CASMutex with timeout support
// and ensures timeout always returns an error with logging
type TimeoutRWMutex struct {
	mu *lock.CASMutex
}

// NewTimeoutRWMutex creates a new TimeoutRWMutex
func NewTimeoutRWMutex() *TimeoutRWMutex {
	return &TimeoutRWMutex{
		mu: lock.NewCASMutex(),
	}
}

// Lock acquires the write lock with no timeout (blocking)
func (t *TimeoutRWMutex) Lock() {
	t.mu.Lock()
}

// Unlock releases the write lock
func (t *TimeoutRWMutex) Unlock() {
	t.mu.Unlock()
}

// RLock acquires the read lock with no timeout (blocking)
func (t *TimeoutRWMutex) RLock() {
	t.mu.RLock()
}

// RUnlock releases the read lock
func (t *TimeoutRWMutex) RUnlock() {
	t.mu.RUnlock()
}

// TryLockWithTimeout tries to acquire the write lock with a timeout.
// Returns ErrLockTimeout if the lock cannot be acquired within the timeout.
// Logs a warning on timeout.
func (t *TimeoutRWMutex) TryLockWithTimeout(timeout time.Duration) error {
	if t.mu.TryLockWithTimeout(timeout) {
		return nil
	}
	log.Printf("TryLockWithTimeout: write lock acquisition timed out after %v", timeout)
	return ErrLockTimeout
}

// TryLockContext tries to acquire the write lock with context support.
// Returns ErrLockTimeout if the context is done before the lock is acquired.
// Logs a warning on timeout.
func (t *TimeoutRWMutex) TryLockContext(ctx context.Context) error {
	if t.mu.TryLockWithContext(ctx) {
		return nil
	}
	log.Printf("TryLockContext: write lock acquisition failed, context done: %v", ctx.Err())
	return ErrLockTimeout
}

// TryRLockWithTimeout tries to acquire the read lock with a timeout.
// Returns ErrRLockTimeout if the lock cannot be acquired within the timeout.
// Logs a warning on timeout.
func (t *TimeoutRWMutex) TryRLockWithTimeout(timeout time.Duration) error {
	if t.mu.RTryLockWithTimeout(timeout) {
		return nil
	}
	log.Printf("TryRLockWithTimeout: read lock acquisition timed out after %v", timeout)
	return ErrRLockTimeout
}

// TryRLockContext tries to acquire the read lock with context support.
// Returns ErrRLockTimeout if the context is done before the lock is acquired.
// Logs a warning on timeout.
func (t *TimeoutRWMutex) TryRLockContext(ctx context.Context) error {
	if t.mu.RTryLockWithContext(ctx) {
		return nil
	}
	log.Printf("TryRLockContext: read lock acquisition failed, context done: %v", ctx.Err())
	return ErrRLockTimeout
}

// LockWithDefaultTimeout acquires the write lock with the default timeout
func (t *TimeoutRWMutex) LockWithDefaultTimeout() error {
	return t.TryLockWithTimeout(DefaultLockTimeout)
}

// RLockWithDefaultTimeout acquires the read lock with the default timeout
func (t *TimeoutRWMutex) RLockWithDefaultTimeout() error {
	return t.TryRLockWithTimeout(DefaultLockTimeout)
}
