package application

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/server"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/providerconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func TestActualSourceRuntimeReplayNoWriteNoRestartAndDeferredNoAck(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	provider := "private-provider"
	base := providerconfig.Selection{ProviderID: &provider, ModelID: "fixture-model", EffortLevel: "low"}
	for _, deferred := range []bool{false, true} {
		changed := false
		d := &DesktopSession{options: Options{RuntimeSelection: &base}, rejectedRuntime: "previously invalid", emit: func(data json.RawMessage) {
			if obj(data).String("type") != "runtime_config_applied" {
				t.Fatal(string(data))
			}
			changed = true
		}}
		if deferred {
			d.deferred = &providerconfig.RuntimeSelection{Selection: base}
		}
		if !d.acceptReplayedRuntime(providerconfig.RuntimeSelection{Selection: base}) || changed == deferred || d.rejectedRuntime != "" {
			t.Fatal("same normalized replay was not admitted without restart", deferred, changed)
		}
		rows := []map[string]any{{"current": map[string]any{"providerId": provider, "modelId": "fixture-model", "effort": "low"}, "next": map[string]any{"providerId": provider, "modelId": "fixture-model", "effort": "low", "openAIFastMode": false}, "deferred": deferred, "active": true}}
		home := t.TempDir()
		fixture, _ := filepath.Abs("../../testdata/native-runtime-replay.fixture.ts")
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
		cmd.Dir = home
		cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_RUNTIME_REPLAY_FIXTURE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
		cmd.Stdin = bytes.NewReader(raw(map[string]any{"rows": rows}))
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatal(err, string(out))
		}
		var source struct {
			Rows []struct {
				Calls    []string
				Deferred bool
			}
		}
		if err = json.Unmarshal(out, &source); err != nil || len(source.Rows) != 1 {
			t.Fatal(err, string(out))
		}
		want := []string{}
		if changed {
			want = append(want, "ack")
		}
		if !reflect.DeepEqual(want, source.Rows[0].Calls) || source.Rows[0].Deferred != deferred {
			t.Fatalf("actual Source%s Goack%v deferred%v", out, changed, deferred)
		}
	}
	for _, next := range []providerconfig.Selection{{ProviderID: &provider, ModelID: "other", EffortLevel: "low"}, {ProviderID: &provider, ModelID: "fixture-model", EffortLevel: "high"}, {ModelID: "fixture-model", EffortLevel: "low"}} {
		if sameNormalizedRuntime(base, next) {
			t.Fatal("different selection incorrectly treated as replay", next)
		}
	}
}
func TestActualDesktopRuntimeReplayRetainsActiveNativeOwnerAndPendingBash(t *testing.T) {
	var calls atomic.Int32
	var target string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			response(w, "pending-replay-bash", "Bash", map[string]any{"command": "printf ACTUAL_REPLAY_BASH_OK > '" + target + "'"}, "")
			return
		}
		response(w, "after-replay-bash", "", nil, "actual command finished after replay")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Arguments.Tools = []string{"Bash"}
	o.Arguments.PermissionMode = "default"
	target = filepath.Join(o.WorkDir, "replay-actual.txt")
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	index := filepath.Join(config, "cc-haha", "providers.json")
	if err := os.MkdirAll(filepath.Dir(index), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(index, raw(map[string]any{"schemaVersion": 6, "activeId": "same", "providers": []any{map[string]any{"id": "same", "presetId": "deepseek", "name": "private runtime replay", "baseUrl": peer.URL, "apiKey": "fake-replay-key", "apiFormat": "anthropic", "authStrategy": "api_key", "models": map[string]string{"main": "fixture-model", "haiku": "", "sonnet": "", "opus": ""}}}}), 0600); err != nil {
		t.Fatal(err)
	}
	provider := "same"
	o.RuntimeSelection = &providerconfig.Selection{ProviderID: &provider, ModelID: "fixture-model"}
	o.ProviderOptions = &providerconfig.Options{Store: storage.New(storage.Options{}), ConfigDir: config}
	frames := make(chan json.RawMessage, 128)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	live, err := DesktopFactory(o)(ctx, server.SessionOptions{ID: o.Arguments.SessionID, WorkDir: o.WorkDir}, func(value json.RawMessage) { frames <- bytes.Clone(value) })
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close(context.Background())
	d := live.(*DesktopSession)
	done := make(chan error, 1)
	go func() {
		done <- d.Handle(ctx, raw(map[string]any{"type": "user_message", "content": "run the actual command after approval"}))
	}()
	var id string
	for id == "" {
		select {
		case value := <-frames:
			if frame := obj(value); frame.String("type") == "permission_request" {
				id = frame.String("requestId")
			}
		case <-ctx.Done():
			t.Fatal("actual Bash permission missing", ctx.Err())
		}
	}
	before := d.currentEngine()
	if err = d.Handle(ctx, raw(map[string]any{"type": "set_runtime_config", "providerId": provider, "modelId": "fixture-model"})); err != nil {
		t.Fatal("replayed runtime issued a forbidden Owner command", err)
	}
	if d.currentEngine() != before || d.deferred != nil || len(before.PendingPermissions()) != 1 || calls.Load() != 1 {
		t.Fatal("replay changed active runtime or permissions")
	}
	ack := false
	for !ack {
		select {
		case value := <-frames:
			ack = obj(value).String("type") == "runtime_config_applied"
		case <-ctx.Done():
			t.Fatal("Source replay did not confirm unchanged runtime", ctx.Err())
		}
	}
	if err = d.Handle(ctx, raw(map[string]any{"type": "permission_response", "requestId": id, "allowed": true})); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	proof, err := os.ReadFile(target)
	if err != nil || string(proof) != "ACTUAL_REPLAY_BASH_OK" || calls.Load() != 2 {
		t.Fatal("same admitted command did not complete", string(proof), err, calls.Load())
	}
}
