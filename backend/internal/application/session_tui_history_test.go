package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestTUIStartupSnapshotDuringActualASKKeepsOwnerContinuationGuard(t *testing.T) {
	reached, release := make(chan struct{}), make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(reached)
		select {
		case <-release:
			response(w, "active-bootstrap", "", nil, "ACTIVE_SNAPSHOT_FINISHED")
		case <-r.Context().Done():
		}
	}))
	defer peer.Close()
	options := nativeSessionOptions(t, peer.URL)
	options.Interactive = true
	session, err := NewSession(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- session.Submit(context.Background(), tui.Input{Text: "actual active startup observer"}, nil)
	}()
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	select {
	case <-reached:
	case err := <-done:
		t.Fatal("actual ASK failed before physical model", err)
	case <-ctx.Done():
		t.Fatal("actual model not reached", ctx.Err())
	}
	// The actual ASK's physical HTTP await has a live continuation. Input
	// commands must stay forbidden; the startup view only copies its pre-ASK
	// snapshot and a legal observer may read hint eligibility on this Owner.
	if err := session.process.Owner.Run(ctx, func(*cooperative.Scope) error { return nil }); err == nil || !strings.Contains(err.Error(), "continuation") {
		t.Fatal("ordinary Owner guard changed", err)
	}
	history, _, err := session.initialTUIHistory(ctx)
	if err != nil || len(history) != 0 {
		t.Fatal("startup snapshot queried active mutable history", history, err)
	}
	if _, err := session.ResumeHint(ctx); err != nil {
		t.Fatal("actual pure hint observer failed during continuation", err)
	}
}

func TestInitialTUIHistoryIsActualControllerSnapshotWithoutReplayOrMutation(t *testing.T) {
	var requests atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		response(w, "original-history", "", nil, "HISTORY_OWNER_ASSISTANT")
	}))
	defer peer.Close()
	options := nativeSessionOptions(t, peer.URL)
	s, err := NewSession(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Submit(context.Background(), tui.Input{Text: "HISTORY_OWNER_USER"}, nil); err != nil {
		t.Fatal(err)
	}
	var original []byte
	var sid string
	if err := s.process.Owner.Run(context.Background(), func(*cooperative.Scope) error {
		sid = s.process.Controller.SessionID()
		original, err = json.Marshal(s.process.Controller.History())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	options.Arguments.Resume, options.Arguments.SessionID = sid, ""
	options.Interactive = true
	s, err = NewSession(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	// Resumption can add Source-owned view metadata. Compare exactly the
	// restored Controller's startup snapshot, never reconstruct disk messages.
	receipt, err := s.process.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(*cooperative.Scope) error {
		data, marshalError := json.Marshal(s.process.Controller.History())
		original = data
		return marshalError
	})
	if err == nil {
		err = receipt.Wait()
	}
	if err != nil {
		t.Fatal(err)
	}
	first, _, err := s.initialTUIHistory(context.Background())
	if err != nil || len(first) < 2 {
		t.Fatal("actual history snapshot", first, err)
	}
	got, _ := json.Marshal(first)
	if !bytes.Equal(original, got) {
		t.Fatal("bootstrap reconstructed instead of copying actual envelopes", string(got), string(original))
	}
	first[0].Set("uiMutation", json.RawMessage(`true`))
	second, _, err := s.initialTUIHistory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got, _ = json.Marshal(second)
	if requests.Load() != 1 || !bytes.Equal(original, got) {
		t.Fatal("display replayed Agent or mutated shared history", requests.Load(), string(got))
	}
}

func TestSessionTUIHistoryAndResumeHintUseSameOwnerActualSnapshotAndSourceGates(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Actual Source Bun required")
	}
	rows := []map[string]any{}
	for _, row := range []struct {
		exists, disabled bool
		title            string
	}{{false, false, ""}, {true, false, ""}, {true, false, `a "title"\backslash`}, {true, true, "hidden title"}} {
		o := nativeSessionOptions(t, "http://127.0.0.1:1")
		o.Arguments.SessionPersistenceDisabled = row.disabled
		s, err := NewSession(context.Background(), o)
		if err != nil {
			t.Fatal(err)
		}
		var sid, path string
		err = s.process.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			sid = s.process.Controller.SessionID()
			var err error
			path, err = s.process.Controller.Writer().ComputedTranscriptPath(scope)
			if err != nil {
				return err
			}
			s.process.Controller.Writer().RestoreMetadata(protocol.NewJSONObject(field("customTitle", row.title)))
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if row.exists {
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		// Each fixture's native OS and config roots are owned and private. There
		// is no model call: initial history and hint only inspect real state.
		history, fullscreen, err := s.initialTUIHistory(context.Background())
		if err != nil || fullscreen || len(history) != 0 {
			t.Fatalf("fresh Controller history/fullscreen %v %v %v", history, fullscreen, err)
		}
		actual, err := s.ResumeHint(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, map[string]any{"sid": sid, "title": row.title, "exists": row.exists, "disabled": row.disabled, "tty": true, "interactive": true, "actual": actual})
		if err := s.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	// Terminal eligibility belongs to Run; the original function also proves
	// that redirected output/noninteractive hosts don't print a hint.
	rows = append(rows, map[string]any{"sid": "non-tty", "title": "", "exists": true, "tty": false, "interactive": true, "actual": ""}, map[string]any{"sid": "non-interactive", "title": "", "exists": true, "tty": true, "interactive": false, "actual": ""})
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-tui-history.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TUI_HISTORY_FIXTURE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1"}
	input, _ := json.Marshal(map[string]any{"home": home, "hints": rows, "fullscreen": []any{}, "histories": []any{}})
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Source hints %v %.4000s", err, out)
	}
	var source struct{ Hints [][]string }
	if err := json.Unmarshal(out, &source); err != nil || len(source.Hints) != len(rows) {
		t.Fatalf("Source hint shape %v %.4000s", err, out)
	}
	for i, row := range rows {
		expected := ""
		if len(source.Hints[i]) > 1 {
			t.Fatal("Source emitted duplicate hint", i, source.Hints[i])
		}
		if len(source.Hints[i]) == 1 {
			expected = source.Hints[i][0]
			if !strings.Contains(expected, "\nclaude --resume ") {
				t.Fatal("Original Source literal changed", expected)
			}
			// Keep the original Source lexical oracle. The only deliberate
			// product adaptation is its installed Go launcher's invocation.
			expected = strings.Replace(expected, "\nclaude --resume ", "\nclaude-haha --resume ", 1)
		}
		if row["actual"] != expected {
			t.Fatal("Source hint mismatch", i, row["actual"], expected)
		}
	}
}
