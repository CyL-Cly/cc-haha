package sessioncontroller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestActualSourceTranscriptNativeReadyDefersAwaitBodyToPromiseReaction(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Bun required")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-transcript-native-io.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	process := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	process.Dir = home
	process.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_TRANSCRIPT_NATIVE_IO=1", "CLAUDE_CODE_SIMPLE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "PATH=" + home}
	process.Stdin = bytes.NewReader(raw(map[string]any{"home": home}))
	var stderr bytes.Buffer
	process.Stderr = &stderr
	output, err := process.Output()
	if err != nil {
		t.Fatal("actual Source prepareTranscriptFile", err, string(output), stderr.String())
	}
	var observed struct {
		Events []struct {
			Phase string
			Opens int
		}
		Blocked  []string
		Physical bool
	}
	if err = json.Unmarshal(output, &observed); err != nil || !observed.Physical || len(observed.Blocked) != 0 || len(observed.Events) != 3 || observed.Events[0].Phase != "native-ready-before-await-body" || observed.Events[0].Opens != 0 || observed.Events[1].Phase != "after-await-body-reaction" || observed.Events[1].Opens != 1 || observed.Events[2].Opens != 1 {
		t.Fatal("Original native Promise completion/await body ordering differs", err, string(output))
	}
	t.Log("actual original native-I/O receipt", string(output))
	owner := cooperative.New()
	defer owner.Close()
	pending := &PendingWriter{owner: owner}
	var reaction *cooperative.Receipt
	completed := false
	err = owner.Run(ctx, func(scope *cooperative.Scope) error {
		var err error
		reaction, err = completeTranscriptNativeIO(scope, pending, func(next *cooperative.Scope) {
			if !next.BelongsTo(owner) {
				t.Error("native result escaped its original owner")
			}
			completed = true
		})
		if completed {
			t.Error("native syscall completion executed the original await body inline; actual Source still awaits its Promise reaction")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if reaction == nil {
		t.Fatal("actual native Promise reaction was not registered")
	}
	if err = reaction.Wait(); err != nil || !completed {
		t.Fatal("actual native await body was lost", err)
	}
}

func TestTranscriptNativeIOReactionRejectsForeignAndMissingCapabilities(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	other := cooperative.New()
	defer other.Close()
	pending := &PendingWriter{owner: owner}
	if _, err := completeTranscriptNativeIO(nil, pending, func(*cooperative.Scope) {}); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("nil callback scope admitted", err)
	}
	called := false
	if err := other.Run(context.Background(), func(scope *cooperative.Scope) error {
		_, err := completeTranscriptNativeIO(scope, pending, func(*cooperative.Scope) { called = true })
		if !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("foreign native completion admitted", err)
		}
		return nil
	}); err != nil || called {
		t.Fatal(err, called)
	}
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		for _, value := range []*PendingWriter{nil, pending} {
			if _, err := completeTranscriptNativeIO(scope, value, nil); !errors.Is(err, cooperative.ErrRestricted) {
				t.Error("missing completion/pending writer admitted", err)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
