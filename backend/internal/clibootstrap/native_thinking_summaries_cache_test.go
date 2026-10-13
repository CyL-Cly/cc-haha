package clibootstrap

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
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestNativeThinkingSummaryCacheMatchesActualSourceAcrossSettingsRefreshAndChild(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	posts := 0
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { posts++; http.Error(w, "unexpected model", 500) }))
	defer peer.Close()
	parent := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		n.Environment["ANTHROPIC_BASE_URL"] = "http://api.anthropic.com"
		n.Environment["HTTP_PROXY"] = peer.URL
		n.Environment["CI"] = "1"
		n.Environment["CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH"] = "1"
		if err := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), []byte(`{"showThinkingSummaries":true}`), 0600); err != nil {
			t.Fatal(err)
		}
	}, func(o *MainPrintOptions) { o.Interactive = true; o.Arguments.Tools = []string{"Read"} })
	ctx := context.Background()
	values := []bool{}
	read := func(m *MainPrint, model string) {
		t.Helper()
		if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			value, err := m.nativeThinkingSummaryBeta(scope, model)
			values = append(values, value)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	read(parent, "claude-sonnet-4-6")
	if err := os.WriteFile(filepath.Join(parent.options.Native.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), []byte(`{"showThinkingSummaries":false}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := parent.Owner.Run(ctx, func(*cooperative.Scope) error { parent.settingState.ResetSettingsCache(); return nil }); err != nil {
		t.Fatal(err)
	}
	read(parent, "claude-sonnet-4-6")
	read(parent, "claude-opus-4-6")
	var childOptions MainPrintOptions
	if err := parent.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		var err error
		permission, err := parent.Host.NativePermissionContext(scope)
		if err != nil {
			return err
		}
		childOptions, err = parent.nativeIndependentAgentOptions(scope, builtintools.AgentRequest{AgentType: "general-purpose", Tools: []string{"Read"}, PermissionContext: permission}, "asummarycache", parent.workingDirectory(), "claude-sonnet-4-6", parent.options.Native.Temp, []json.RawMessage{quoteMain("actual parent system")}, nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if childOptions.thinkingSummaryState != parent.options.thinkingSummaryState {
		t.Fatal("in-process child lost Source global model beta cache")
	}
	childOptions.Output, childOptions.Error, childOptions.OnOutput = io.Discard, io.Discard, nil
	child, err := NewMainPrint(childOptions)
	if err != nil {
		t.Fatal(err)
	}
	defer child.ShutdownSession(ctx)
	if err = child.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	read(child, "claude-sonnet-4-6")
	read(child, "claude-haiku-4-5")
	for _, base := range []string{"https://private-relay.invalid", "http://api.anthropic.com", "https://private-relay.invalid", "http://api.anthropic.com"} {
		if err = parent.Owner.Run(ctx, func(*cooperative.Scope) error {
			return parent.environment.SetProperty(quoteMain("ANTHROPIC_BASE_URL"), quoteMain(base))
		}); err != nil {
			t.Fatal(err)
		}
		read(parent, "claude-sonnet-4-7")
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-thinking-summaries.fixture.ts")
	home := t.TempDir()
	rows := []map[string]any{}
	for index, model := range []string{"claude-sonnet-4-6", "claude-sonnet-4-6", "claude-opus-4-6", "claude-sonnet-4-6", "claude-haiku-4-5"} {
		rows = append(rows, map[string]any{"interactive": true, "model": model, "settings": map[string]any{"showThinkingSummaries": index == 0}, "retainCache": index != 0})
	}
	for _, base := range []string{"https://private-relay.invalid", "http://api.anthropic.com", "https://private-relay.invalid", "http://api.anthropic.com"} {
		rows = append(rows, map[string]any{"interactive": true, "model": "claude-sonnet-4-7", "settings": map[string]any{"showThinkingSummaries": false}, "retainCache": true, "env": map[string]string{"ANTHROPIC_BASE_URL": base}})
	}
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_THINKING_SUMMARIES_FIXTURE=1", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
	cmd.Stdin = bytes.NewReader(payload)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	var actual []struct{ Redacted bool }
	if err = json.Unmarshal(out, &actual); err != nil || len(actual) != len(values) {
		t.Fatal(err, string(out))
	}
	want := []bool{}
	for _, row := range actual {
		want = append(want, row.Redacted)
	}
	if !reflect.DeepEqual(values, want) || posts != 0 {
		t.Fatal("Source cache/child/isolation did not match", values, want, posts)
	}
}
