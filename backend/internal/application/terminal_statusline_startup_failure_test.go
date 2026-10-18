//go:build !windows

package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

var statusLineStartupFailure = errors.New("controlled subsequent native TUI startup binding failure")

type statusLineStartupFailureSession struct {
	*Session
	home      string
	held      bool
	completed chan struct{}
	pid       int
}

func (s *statusLineStartupFailureSession) SubscribeTerminalStatusLineMount(ctx context.Context, mounted bool, vim string, listener func(json.RawMessage)) (json.RawMessage, func(), error) {
	return s.Session.SubscribeTerminalStatusLineMount(ctx, mounted, vim, func(value json.RawMessage) {
		listener(value)
		if bytes.Contains(value, []byte("BEFORE_RUN_READY")) {
			select {
			case s.completed <- struct{}{}:
			default:
			}
		}
	})
}
func (s *statusLineStartupFailureSession) BindTerminalAutoConsent(ctx context.Context, _ tui.Emit) (*tui.PermissionRequest, func(), error) {
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	if !s.held {
		select {
		case <-s.completed:
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-deadline.C:
			return nil, nil, errors.New("actual completed statusline callback blocked before Program.Run")
		}
	}
	for {
		rows := readStatusLineStartupRows(s.home)
		if len(rows) > 0 {
			s.pid, _ = strconv.Atoi(strings.SplitN(rows[0], " ", 2)[0])
			return nil, nil, statusLineStartupFailure
		}
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-deadline.C:
			return nil, nil, errors.New("actual held statusline did not start before Program.Run")
		case <-time.After(time.Millisecond):
		}
	}
}
func readStatusLineStartupRows(home string) []string {
	data, _ := os.ReadFile(filepath.Join(home, "statusline-records"))
	if len(data) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}
func TestActualTUIStatusLineBeforeProgramRunFailureJoinsHeldChildAndQueuedCallback(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(map[bool]string{false: "completed-callback", true: "held-child"}[held], func(t *testing.T) {
			peer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("startup failure entered unowned model HTTP") }))
			defer peer.Close()
			body := "printf BEFORE_RUN_READY\n"
			if held {
				body = "/bin/sleep 60\nprintf FORBIDDEN_LATE\n"
			}
			s, home := newTerminalStatusLinePhysicalSession(t, peer.URL, body)
			wrapper := &statusLineStartupFailureSession{Session: s, home: home, held: held, completed: make(chan struct{}, 1)}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			finished := make(chan error, 1)
			go func() {
				finished <- tui.Run(ctx, wrapper, tui.Options{Input: strings.NewReader(""), Output: &bytes.Buffer{}, Width: 100, Height: 35})
			}()
			select {
			case err := <-finished:
				if !errors.Is(err, statusLineStartupFailure) {
					t.Fatal("actual startup failure/cancel consumer", err)
				}
			case <-ctx.Done():
				t.Fatal("actual pre-Run teardown blocked on owned statusline", ctx.Err())
			}
			if wrapper.pid <= 0 {
				t.Fatal("actual owned shell PID absent")
			}
			if err := syscall.Kill(wrapper.pid, 0); !errors.Is(err, syscall.ESRCH) {
				t.Fatal("pre-Run failure retained actual child after joined teardown", wrapper.pid, err)
			}
			if len(readStatusLineStartupRows(home)) != 1 {
				t.Fatal("unexpected startup command reinvocation")
			}
		})
	}
}
