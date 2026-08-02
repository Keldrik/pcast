package platform

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestLockAcquireRelease(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pcast.lock")
	l := NewLock(path)
	ctx := context.Background()
	if err := l.Acquire(ctx); err != nil {
		t.Fatal(err)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestLockContentionTimeout(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pcast.lock")
	a := NewLock(path)
	b := NewLock(path)
	if err := a.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Release() }()

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	err := b.Acquire(ctx)
	if err == nil {
		_ = b.Release()
		t.Fatal("expected lock timeout/cancellation")
	}
}

func TestLockCancellation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pcast.lock")
	a := NewLock(path)
	b := NewLock(path)
	if err := a.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Release() }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.Acquire(ctx); err == nil {
		_ = b.Release()
		t.Fatal("expected cancellation error")
	}
}

func TestWithLock(t *testing.T) {
	dir := t.TempDir()
	l := NewLock(filepath.Join(dir, "pcast.lock"))
	called := false
	err := l.WithLock(context.Background(), func(ctx context.Context) error {
		called = true
		return nil
	})
	if err != nil || !called {
		t.Fatalf("err=%v called=%v", err, called)
	}
}
