package imruntime

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

type migrationContextKey struct{}

func migrationRuntimeAvailable(config Config) error {
	if config.Environment["CC_HAHA_MIGRATION_VALIDATION"] == "1" {
		return errors.New("Data migration validation prohibits adapter startup")
	}
	return nil
}

type MigrationLifecycle struct {
	mu        sync.Mutex
	quiescing bool
	pending   int
	idle      chan struct{}
	stopping  chan struct{}
	failures  []error
}

func NewMigrationLifecycle() *MigrationLifecycle {
	idle := make(chan struct{})
	close(idle)
	return &MigrationLifecycle{idle: idle}
}
func newMigrationContext(ctx context.Context) (context.Context, *MigrationLifecycle) {
	lifecycle := NewMigrationLifecycle()
	return context.WithValue(ctx, migrationContextKey{}, lifecycle), lifecycle
}
func migrationFromContext(ctx context.Context) *MigrationLifecycle {
	if ctx == nil {
		return nil
	}
	value, _ := ctx.Value(migrationContextKey{}).(*MigrationLifecycle)
	return value
}
func (l *MigrationLifecycle) Available() bool {
	if l == nil {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return !l.quiescing
}

// Track accepts writes from an already admitted handler after ingress closes.
// It records the actual disk/credential outcome, rather than response delivery.
func (l *MigrationLifecycle) Track() func(error) {
	if l == nil {
		return func(error) {}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.trackLocked()
}

// Admit atomically closes incoming platform work at the maintenance boundary.
// Quiesce still joins handlers admitted before the boundary was closed.
func (l *MigrationLifecycle) Admit() (func(error), bool) {
	if l == nil {
		return func(error) {}, true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.quiescing {
		return nil, false
	}
	return l.trackLocked(), true
}

func (l *MigrationLifecycle) trackLocked() func(error) {
	if l.pending == 0 {
		l.idle = make(chan struct{})
	}
	l.pending++
	var once sync.Once
	return func(err error) {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			if l.quiescing && err != nil {
				l.failures = append(l.failures, err)
			}
			l.pending--
			if l.pending == 0 {
				close(l.idle)
			}
		})
	}
}
func (l *MigrationLifecycle) RecordFailure(err error) {
	if l == nil || err == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.quiescing {
		l.failures = append(l.failures, err)
	}
}
func (l *MigrationLifecycle) Quiesce(ctx context.Context, closeOwned func()) error {
	if l == nil {
		closeOwned()
		return nil
	}
	l.mu.Lock()
	if l.stopping == nil {
		l.quiescing = true
		l.stopping = make(chan struct{})
		go func() {
			func() {
				defer func() {
					if p := recover(); p != nil {
						l.RecordFailure(fmt.Errorf("Adapter shutdown failed: %v", p))
					}
				}()
				closeOwned()
			}()
			for {
				l.mu.Lock()
				idle, pending := l.idle, l.pending
				l.mu.Unlock()
				if pending == 0 {
					break
				}
				<-idle
			}
			close(l.stopping)
		}()
	}
	done := l.stopping
	l.mu.Unlock()
	select {
	case <-done:
		l.mu.Lock()
		defer l.mu.Unlock()
		return errors.Join(l.failures...)
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (r *FeishuRuntime) Quiesce(ctx context.Context) error { return r.migration.Quiesce(ctx, r.Close) }
func (r *TelegramRuntime) Quiesce(ctx context.Context) error {
	return r.migration.Quiesce(ctx, r.Close)
}
func (r *SlackRuntime) Quiesce(ctx context.Context) error  { return r.migration.Quiesce(ctx, r.Close) }
func (r *WechatRuntime) Quiesce(ctx context.Context) error { return r.migration.Quiesce(ctx, r.Close) }
func (r *QQRuntime) Quiesce(ctx context.Context) error     { return r.migration.Quiesce(ctx, r.Close) }
func (r *WecomRuntime) Quiesce(ctx context.Context) error  { return r.migration.Quiesce(ctx, r.Close) }
func (r *WhatsAppRuntime) Quiesce(ctx context.Context) error {
	return r.migration.Quiesce(ctx, r.Close)
}
func (r *DingTalkRuntime) Quiesce(ctx context.Context) error {
	return r.migration.Quiesce(ctx, r.Close)
}
