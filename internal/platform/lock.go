package platform

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"

	"github.com/Keldrik/pcast/internal/model"
)

// DefaultLockWait is the bounded wait for the application lock.
const DefaultLockWait = 10 * time.Second

// Lock is a cross-platform exclusive application lock.
type Lock struct {
	path string
	fl   *flock.Flock
}

// NewLock creates a lock for the given path (file is created on acquire).
func NewLock(path string) *Lock {
	return &Lock{path: path, fl: flock.New(path)}
}

// Acquire obtains the exclusive lock, waiting up to the timeout or until ctx is done.
func (l *Lock) Acquire(ctx context.Context) error {
	if l == nil || l.fl == nil {
		return model.LockUnavailable("lock is not configured", nil)
	}
	// Ensure parent directory exists.
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return model.LockUnavailable("create lock directory", err)
	}

	deadline := time.Now().Add(DefaultLockWait)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}

	for {
		if err := ctx.Err(); err != nil {
			return model.LockUnavailable("lock cancelled", err)
		}
		ok, err := l.fl.TryLock()
		if err != nil {
			return model.LockUnavailable("acquire lock", err)
		}
		if ok {
			return nil
		}
		if time.Now().After(deadline) {
			return model.LockUnavailable(fmt.Sprintf("lock unavailable after %s", DefaultLockWait), nil)
		}
		select {
		case <-ctx.Done():
			return model.LockUnavailable("lock cancelled", ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// Release frees the lock.
func (l *Lock) Release() error {
	if l == nil || l.fl == nil {
		return nil
	}
	if err := l.fl.Unlock(); err != nil {
		return model.LockUnavailable("release lock", err)
	}
	return nil
}

// WithLock runs fn while holding the application lock.
func (l *Lock) WithLock(ctx context.Context, fn func(context.Context) error) error {
	if err := l.Acquire(ctx); err != nil {
		return err
	}
	defer func() { _ = l.Release() }()
	return fn(ctx)
}
