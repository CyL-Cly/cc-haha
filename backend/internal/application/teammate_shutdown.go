package application

import (
	"context"
	"errors"
	"fmt"
	"time"
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
	return errors.Join(d.closeSDKTransport(ctx), engine.Close(ctx))
}
