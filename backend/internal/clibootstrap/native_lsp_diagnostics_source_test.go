package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/attachments"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestActualSourceLSPDiagnosticsPeerRegistryLimitsDedupMainThreadAndRenderer(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual pinned Source required")
	}
	diagnostics := []protocol.JSONObject{}
	for i := 0; i < 15; i++ {
		message := quoteMain("diagnostic " + strings.Repeat("正文", 20))
		if i == 0 {
			message = json.RawMessage(`"LONG_RAW_` + strings.Repeat("x", 3990) + `\ud800\udfff"`)
		}
		diagnostics = append(diagnostics, protocol.NewJSONObject(fieldMain("range", map[string]any{"start": map[string]int{"line": i, "character": 2}, "end": map[string]int{"line": i, "character": 4}}), fieldMain("severity", i%4+1), fieldMain("source", "private-real-peer"), fieldMain("code", i), protocol.JSONField{Name: "message", Value: message}))
	}
	f, m, receipt := newNativeLSPLifecycleFixture(t, false, false, false, func(nativePluginFixture) map[string]string {
		return map[string]string{"CC_HAHA_LSP_DIAGNOSTIC": string(rawMain(diagnostics))}
	})
	defer m.Close()
	files := []string{}
	for _, name := range []string{"first space 中文.txt", "second.txt", "third.txt", "fourth.txt"} {
		file := filepath.Join(f.n.CWD, name)
		f.write(t, file, "actual diagnostic source text\n")
		files = append(files, file)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-lsp-runtime.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = f.home
	cmd.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_LSP_RUNTIME_SOURCE=1", "NODE_ENV=production", "USER_TYPE=external", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1", "CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION=false", "ANTHROPIC_API_KEY=private-only-lsp", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"home": f.home, "cwd": f.n.CWD, "peer": filepath.Join(f.plugin, "bin", "private-lsp-peer"), "receipt": receipt, "file": files[0], "diagnosticFiles": files}))
	source, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal("original actual diagnostic consumer", err, string(source))
	}
	var original struct {
		Observations []struct {
			Payloads   []protocol.JSONObject
			Normalized []protocol.JSONObject
		}
		Capabilities struct{ Blocked, SecureReads []string }
	}
	if err = json.Unmarshal(source, &original); err != nil {
		t.Fatal(err, string(source))
	}
	if len(original.Observations) != 6 || len(original.Capabilities.Blocked) != 0 {
		t.Fatal("original Source diagnostic profile", string(source))
	}
	if len(original.Observations[0].Payloads) != 0 || len(original.Observations[1].Payloads) != 0 || len(original.Observations[2].Payloads) != 1 || len(original.Observations[3].Payloads) != 1 || len(original.Observations[4].Payloads) != 0 || len(original.Observations[5].Payloads) != 1 {
		t.Fatal("original diagnostic producer sequence", string(source))
	}
	if err = os.Remove(receipt); err != nil {
		t.Fatal(err)
	}
	if err = m.nativeLSP.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if err = m.nativeLSP.Open(file, quoteMain("actual diagnostic source text\n")); err != nil {
			t.Fatal(err)
		}
	}
	barrier := func() {
		t.Helper()
		if _, _, err = m.nativeLSP.Request(files[0], "$/fixture/barrier", map[string]any{}); err != nil {
			t.Fatal(err)
		}
	}
	barrier()
	if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		for _, agent := range []string{"", "actual-child"} {
			out, e := m.nativeLSPAttachments(scope, agent)
			if e != nil {
				return e
			}
			if len(out) != 0 {
				t.Fatal("noBash/child consumed actual pending diagnostics")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	compare := func(index int) {
		t.Helper()
		rows := m.nativeLSP.TakeDiagnostics()
		want := original.Observations[index]
		if !reflect.DeepEqual(lspWireCanonicalJSON(rawMain(rows)), lspWireCanonicalJSON(rawMain(want.Payloads))) {
			t.Fatalf("actual registry step%d\nGo=%s\nSource=%s", index, rawMain(rows), rawMain(want.Payloads))
		}
		normalized := []protocol.JSONObject{}
		for _, row := range rows {
			projected, e := attachments.ProjectWithOptions(rawMain(row), m.factory, &attachments.PlanOptions{})
			if e != nil {
				t.Fatal(e)
			}
			for _, record := range projected {
				normalized = append(normalized, protocol.NewJSONObject(protocol.JSONField{Name: "content", Value: mainObj(record.Get("message")).Get("content")}, protocol.JSONField{Name: "isMeta", Value: record.Get("isMeta")}))
			}
		}
		if !reflect.DeepEqual(lspWireCanonicalJSON(rawMain(normalized)), lspWireCanonicalJSON(rawMain(want.Normalized))) {
			t.Fatalf("actual renderer step%d\nGo=%s\nSource=%s", index, rawMain(normalized), rawMain(want.Normalized))
		}
	}
	compare(2)
	if err = m.nativeLSP.Change(files[0], quoteMain("same diagnostic again")); err != nil {
		t.Fatal(err)
	}
	barrier()
	compare(3)
	m.nativeLSP.NotifyChange(files[0], quoteMain("literal URI clear retains actual path-key dedup"))
	if err = m.nativeLSP.JoinNotifications(ctx); err != nil {
		t.Fatal(err)
	}
	barrier()
	compare(4)
	m.nativeLSP.ResetDiagnostics()
	if err = m.nativeLSP.Change(files[0], quoteMain("actual reset permits diagnostics")); err != nil {
		t.Fatal(err)
	}
	barrier()
	compare(5)
}
