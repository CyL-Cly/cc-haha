package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

// The request originates from the worker Host only after its tool response is
// persisted/projected. Close waits for this worker's turn to unwind, then uses
// the same cleanup, transcript drain and owner lifecycle as normal shutdown.
func (s *Session) requestTeammateShutdown(context.Context) error {
	s.mu.Lock()
	if s.teammateShutdownDone != nil {
		s.mu.Unlock()
		return nil
	}
	done := make(chan struct{})
	s.teammateShutdownDone = done
	s.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := s.Close(ctx)
		s.mu.Lock()
		s.teammateShutdownError = err
		s.mu.Unlock()
		close(done)
	}()
	return nil
}

func (d *DesktopSession) requestTeammateShutdown(_ context.Context, expected *Session) error {
	// Desktop Close also closes the belonging SDK transport and pending controls.
	// The ordinary leader does not receive this callback from its factory.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := d.closeDesktopEngine(ctx, expected)
		if err == nil {
			d.mu.Lock()
			owned := d.closed && d.engine == expected
			retire := d.onRuntimeClosed
			d.mu.Unlock()
			// The engine has already joined its own owner and SDK. Calling the
			// server lifecycle before releasing runtimeMu would Close this same
			// wrapper recursively and deadlock graceful shutdown.
			if owned && retire != nil {
				err = retire(ctx, d)
			}
		}
		if err != nil {
			d.mu.Lock()
			stderr := d.options.Stderr
			d.mu.Unlock()
			if stderr != nil {
				_, _ = fmt.Fprintln(stderr, err)
			}
		}
	}()
	return nil
}

// The expected engine check and Close share the original runtime lock. A
// delayed exit request from an older worker cannot close a replacement Host.
func (d *DesktopSession) closeDesktopEngine(ctx context.Context, expected *Session) error {
	d.runtimeMu.Lock()
	defer d.runtimeMu.Unlock()
	if expected != nil && d.engine != expected {
		return expected.Close(ctx)
	}
	d.mu.Lock()
	d.closed = true
	d.deferred = nil
	engine := d.engine
	d.mu.Unlock()
	if d.autoQuestionUnbind != nil {
		d.autoQuestionUnbind()
	}
	engine.Cancel()
	engine.mu.Lock()
	alreadyClosing := engine.closed
	engine.mu.Unlock()
	var cleanup error
	if !alreadyClosing {
		cleanup = engine.process.StopAllBackgroundTasks(ctx)
	}
	transportErr := d.closeSDKTransport(ctx)
	closeErr := engine.Close(ctx)
	// Replacement and teammate shutdown can close this Session independently.
	// Its established Close receipt is authoritative only after the actual
	// cleanup/transcript/Owner join succeeded. Preserve every other failure.
	if cleanup == cooperative.ErrClosed && closeErr == nil {
		cleanup = nil
	}
	return errors.Join(transportErr, cleanup, closeErr)
}
