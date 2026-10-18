package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/lspruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestActualSourceLSPNineOperationsSameNativeToolCallsFramesAndRawProse(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Bun required")
	}
	var resultMap protocol.JSONObject
	file := ""
	results := func(f nativePluginFixture) map[string]string {
		file = filepath.Join(f.n.CWD, "code space 中文.txt")
		uri := lspruntime.URI(file)
		rangeValue := `{"start":{"line":1,"character":2},"end":{"line":1,"character":5}}`
		location := `{"uri":` + string(quoteMain(uri)) + `,"range":` + rangeValue + `}`
		item := `{"name":"fixture\ud800函数","kind":12,"uri":` + string(quoteMain(uri)) + `,"range":` + rangeValue + `,"selectionRange":` + rangeValue + `,"detail":"raw\udfff"}`
		resultMap = protocol.NewJSONObject(protocol.JSONField{Name: "textDocument/definition", Value: json.RawMessage(`[{"targetUri":` + string(quoteMain(uri)) + `,"targetRange":` + rangeValue + `}]`)}, protocol.JSONField{Name: "textDocument/references", Value: json.RawMessage(`[` + location + `,` + location + `]`)}, protocol.JSONField{Name: "textDocument/hover", Value: json.RawMessage(`{"contents":[{"language":"typescript","value":"type \ud800"},"tail\udfff"],"range":` + rangeValue + `}`)}, protocol.JSONField{Name: "textDocument/documentSymbol", Value: json.RawMessage(`[{"name":"outer\ud800","kind":5,"detail":"type\udfff","range":` + rangeValue + `,"children":[{"name":"inner","kind":6,"range":` + rangeValue + `}]}]`)}, protocol.JSONField{Name: "workspace/symbol", Value: json.RawMessage(`[{"name":"fixture\ud800","kind":12,"containerName":"space\udfff","location":` + location + `}]`)}, protocol.JSONField{Name: "textDocument/implementation", Value: json.RawMessage(location)}, protocol.JSONField{Name: "textDocument/prepareCallHierarchy", Value: json.RawMessage(`[` + item + `]`)}, protocol.JSONField{Name: "callHierarchy/incomingCalls", Value: json.RawMessage(`[{"from":` + item + `,"fromRanges":[` + rangeValue + `]}]`)}, protocol.JSONField{Name: "callHierarchy/outgoingCalls", Value: json.RawMessage(`[{"to":` + item + `,"fromRanges":[` + rangeValue + `]}]`)})
		f.n.Environment["ENABLE_LSP_TOOL"] = "1"
		return map[string]string{"CC_HAHA_LSP_RESULTS": string(rawMain(resultMap)), "CC_HAHA_LSP_IGNORE_EXIT": "1"}
	}
	f, previous, receipt := newNativeLSPLifecycleFixture(t, false, false, false, results)
	previous.Close()
	peerPath := filepath.Join(f.plugin, "bin", "private-lsp-peer")
	peerBytes, e := os.ReadFile(peerPath)
	if e != nil {
		t.Fatal(e)
	}
	peerScript := strings.Replace(string(peerBytes), "import json\n", "import json\nimport signal\n", 1)
	peerScript = strings.Replace(peerScript, "while True:\n    length = None", `def terminated(signum, _frame):
    with receipt.open('a', encoding='utf-8') as output:
        output.write(json.dumps({'pid': os.getpid(), 'method': '$/fixture/terminated', 'params': {'signal': signum}}) + '\n')
    os._exit(0)
signal.signal(signal.SIGTERM, terminated)

while True:
    length = None`, 1)
	peerScript = strings.Replace(peerScript, "if method == 'exit':", "if method == 'exit' and os.environ.get('CC_HAHA_LSP_IGNORE_EXIT') != '1':", 1)
	if e = os.WriteFile(peerPath, []byte(peerScript), 0700); e != nil {
		t.Fatal(e)
	}

	git := filepath.Join(f.home, "bin", "git")
	gitBytes, e := os.ReadFile("/usr/bin/git")
	if e != nil {
		t.Fatal(e)
	}
	f.write(t, git, string(gitBytes))
	if e = os.Chmod(git, 0700); e != nil {
		t.Fatal(e)
	}
	f.write(t, filepath.Join(f.home, "empty-git-config"), "")
	canonicalGit, err := filepath.EvalSymlinks(git)
	if err != nil {
		t.Fatal(err)
	}
	f.n.Environment["PATH"] = filepath.Dir(canonicalGit) + ":/usr/bin:/bin"
	f.n.Environment["GIT_CONFIG_NOSYSTEM"] = "1"
	f.n.Environment["CLAUDE_CODE_TMPDIR"] = f.n.Temp
	f.n.Environment["GIT_CONFIG_GLOBAL"] = filepath.Join(f.home, "empty-git-config")
	f.write(t, file, "actual original source text\n")
	calls := []protocol.JSONObject{}
	for _, operation := range []string{"goToDefinition", "findReferences", "hover", "documentSymbol", "workspaceSymbol", "goToImplementation", "prepareCallHierarchy", "incomingCalls", "outgoingCalls"} {
		calls = append(calls, protocol.NewJSONObject(fieldMain("operation", operation), fieldMain("filePath", file), fieldMain("line", 2), fieldMain("character", 3)))
	}

	outside := filepath.Join(f.home, "outside.txt")
	f.write(t, outside, "outside project but private\n")
	permissionRows := []protocol.JSONObject{}
	permissionRow := func(mode, path string, allow, deny, ask []string) {
		if allow == nil {
			allow = []string{}
		}
		if deny == nil {
			deny = []string{}
		}
		if ask == nil {
			ask = []string{}
		}
		request := calls[0].Clone()
		request.Set("filePath", quoteMain(path))
		permissionRows = append(permissionRows, protocol.NewJSONObject(fieldMain("mode", mode), fieldMain("allow", allow), fieldMain("deny", deny), fieldMain("ask", ask), fieldMain("input", request)))
	}
	insideRule := "Read(**/code space 中文.txt)"
	outsideRule := "Edit(**/outside.txt)"
	permissionRow("default", file, nil, nil, nil)
	permissionRow("default", outside, nil, nil, nil)
	permissionRow("default", file, nil, []string{insideRule}, nil)
	permissionRow("default", file, []string{"LSP"}, []string{insideRule}, nil)
	permissionRow("default", file, []string{"LSP"}, nil, []string{insideRule})
	permissionRow("bypassPermissions", file, nil, nil, []string{insideRule})
	permissionRow("bypassPermissions", file, nil, []string{insideRule}, nil)
	permissionRow("default", file, nil, []string{insideRule}, []string{"LSP"})
	permissionRow("default", file, []string{"LSP"}, []string{"LSP"}, nil)
	permissionRow("default", outside, []string{"LSP"}, nil, nil)
	permissionRow("default", outside, []string{outsideRule}, nil, nil)
	permissionRow("dontAsk", outside, nil, nil, nil)
	permissionRow("default", file, nil, []string{"Read"}, nil)
	schemaInputs := []json.RawMessage{rawMain(calls[0])}
	for _, mutation := range []func(*protocol.JSONObject){func(o *protocol.JSONObject) { o.Set("operation", quoteMain("unknown")) }, func(o *protocol.JSONObject) { o.Set("line", rawMain(0)) }, func(o *protocol.JSONObject) { o.Set("character", rawMain(1.5)) }, func(o *protocol.JSONObject) { o.Set("line", quoteMain("2")) }, func(o *protocol.JSONObject) { o.Set("extra", rawMain(true)) }, func(o *protocol.JSONObject) { o.Delete("filePath") }} {
		input := calls[0].Clone()
		mutation(&input)
		schemaInputs = append(schemaInputs, rawMain(input))
	}
	schemaInputs = append(schemaInputs, json.RawMessage(`null`), json.RawMessage(`[]`))
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-lsp-runtime.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = f.home
	cmd.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "CLAUDE_CODE_TMPDIR=" + f.n.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_LSP_RUNTIME_SOURCE=1", "NODE_ENV=production", "USER_TYPE=external", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1", "CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION=false", "ANTHROPIC_API_KEY=private-only-lsp", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "ENABLE_LSP_TOOL=1", "PATH=" + f.n.Environment["PATH"], "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + filepath.Join(f.home, "empty-git-config")}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"home": f.home, "cwd": f.n.CWD, "peer": filepath.Join(f.plugin, "bin", "private-lsp-peer"), "receipt": receipt, "file": file, "toolCalls": calls, "git": git, "observeShutdown": true, "permissionRows": permissionRows, "schemaInputs": schemaInputs}))
	source, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal("actual original nine-op consumer", err, string(source))
	}
	var original struct {
		Results           []protocol.JSONObject
		PermissionResults []protocol.JSONObject
		Parses            []protocol.JSONObject
		Frames            []protocol.JSONObject
		FinalFrames       []protocol.JSONObject
		NativeKills       []protocol.JSONObject
		Catalog           struct{ Declared, Enabled bool }
		Capabilities      struct{ Blocked, SecureReads []string }
	}
	if err = json.Unmarshal(source, &original); err != nil {
		t.Fatal(err, string(source))
	}
	if !original.Catalog.Declared || !original.Catalog.Enabled || len(original.Results) != 9 || len(original.Capabilities.Blocked) != 0 || !reflect.DeepEqual(original.Capabilities.SecureReads, []string{"read"}) {
		t.Fatal("actual original tool premise", string(source))
	}
	if len(original.NativeKills) != 1 || original.NativeKills[0].String("signal") != "SIGTERM" {
		t.Fatal("actual Source default native termination", string(source))
	}
	// Source's fire-and-forget process termination may settle after stop returns;
	// the observed OS signal is not a business synchronization boundary.
	if err = os.Remove(receipt); err != nil {
		t.Fatal(err)
	}
	seedNativeReleaseWarmFixture(t, f, "fixture")
	var global protocol.JSONObject
	globalData, e := os.ReadFile(filepath.Join(f.config, ".claude.json"))
	if e != nil {
		t.Fatal(e)
	}
	if e = global.UnmarshalJSON(globalData); e != nil {
		t.Fatal(e)
	}
	global.Set("projects", rawMain(map[string]any{f.n.CWD: map[string]bool{"hasTrustDialogAccepted": true}}))
	f.write(t, filepath.Join(f.config, ".claude.json"), string(rawMain(global)))
	disabled := false
	m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", Tools: []string{"Read,LSP"}, MaxTurns: 8, PermissionMode: "bypassPermissions", Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Platform: runtime.GOOS, Version: "fixture", Interactive: true, Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err = m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	if err = m.nativeLSP.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		_, tools, err := m.nativeTools.Catalog(scope)
		if err != nil {
			return err
		}
		var tool *queryloop.RegisteredTool
		for i := range tools {
			if tools[i].Name == "LSP" {
				tool = &tools[i]
			}
		}
		if tool == nil {
			return io.ErrUnexpectedEOF
		}
		for i, input := range calls {
			parsed, err := tool.ParseInput(input)
			if err != nil {
				return err
			}
			if err = tool.ValidateInput(ctx, parsed, &builtinwrite.CallContext{Scope: scope}); err != nil {
				return err
			}
			permission, err := m.nativeTools.PermissionDecision(scope, "LSP", &parsed)
			if err != nil {
				return err
			}
			if permission.Behavior != "allow" {
				return fmt.Errorf("native LSP permission %s", permission.Behavior)
			}
			future, err := tool.StartCallJSON(ctx, parsed, queryloop.JSONToolCallContext{Opaque: &builtinwrite.CallContext{Scope: scope}})
			if err != nil {
				return err
			}
			output, err := future.Await(scope)
			if err != nil {
				return err
			}
			mapped, err := tool.MapResultJSON(output.Data, quoteMain("actual-lsp-tool-id"))
			if err != nil {
				return err
			}
			want := original.Results[i]
			if !reflect.DeepEqual(protocol.StringifyValidatedJavaScriptJSON(output.Data), protocol.StringifyValidatedJavaScriptJSON(want.Get("data"))) || !reflect.DeepEqual(protocol.StringifyValidatedJavaScriptJSON(rawMain(mapped)), protocol.StringifyValidatedJavaScriptJSON(want.Get("mapped"))) {
				return fmt.Errorf("original operation %s\nGo=%s\nSource=%s", input.String("operation"), output.Data, want.Get("data"))
			}
		}
		for i, row := range permissionRows {
			if err = m.Host.SetNativePermissionMode(scope, row.String("mode")); err != nil {
				return err
			}
			updates := []protocol.JSONObject{}
			for _, mapping := range []struct{ key, behavior string }{{"allow", "allow"}, {"deny", "deny"}, {"ask", "ask"}} {
				var rules []string
				_ = json.Unmarshal(row.Get(mapping.key), &rules)
				parsedRules := []protocol.JSONObject{}
				for _, rule := range rules {
					tool, content := rule, ""
					if at := strings.IndexByte(rule, '('); at >= 0 {
						tool = rule[:at]
						content = strings.TrimSuffix(rule[at+1:], ")")
					}
					r := protocol.NewJSONObject(fieldMain("toolName", tool))
					if content != "" {
						r.Set("ruleContent", quoteMain(content))
					}
					parsedRules = append(parsedRules, r)
				}
				updates = append(updates, protocol.NewJSONObject(fieldMain("type", "replaceRules"), fieldMain("destination", "session"), fieldMain("behavior", mapping.behavior), fieldMain("rules", parsedRules)))
			}
			if err = m.Host.ApplyNativePermissionUpdates(scope, rawMain(updates)); err != nil {
				return err
			}
			in := mainObj(row.Get("input"))
			decision, e := m.nativeTools.PermissionDecision(scope, "LSP", &in)
			if e != nil {
				return e
			}
			want := original.PermissionResults[i]
			if decision.Behavior != want.String("behavior") || string(decision.Message) != string(want.Get("message")) {
				permissionContext, _ := m.nativeToolPermissionContext(scope)
				return fmt.Errorf("original permission row%d %s\nGo=%s/%s\nSource=%s\nContext=%s updates=%s", i, rawMain(row), decision.Behavior, decision.Message, rawMain(want), rawMain(permissionContext), rawMain(updates))
			}
		}
		for i, value := range schemaInputs {
			parsed, e := toolschema.ParseInputJSON(toolschema.LSPDefinition(), value)
			actual := protocol.NewJSONObject(fieldMain("success", e == nil))
			if e == nil {
				actual.Set("data", rawMain(parsed))
			} else {
				var validation *toolschema.ValidationError
				if !errors.As(e, &validation) {
					return e
				}
				actual.Set("issues", validation.Issues)
			}
			if !reflect.DeepEqual(lspWireCanonicalJSON(rawMain(actual)), lspWireCanonicalJSON(rawMain(original.Parses[i]))) {
				return fmt.Errorf("original strictschema row%d\nGo=%s\nSource=%s", i, rawMain(actual), rawMain(original.Parses[i]))
			}
		}

		return nil
	})
	if err != nil {
		t.Fatal("actual Native tool", err)
	}
	if _, _, err = m.nativeLSP.Request(file, "$/fixture/barrier", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if err = m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
	frames := nativeLSPPhysicalFrames(t, receipt)
	terminated := false
	for _, frame := range frames {
		if frame.String("method") == "$/fixture/terminated" {
			terminated = string(mainObj(frame.Get("params")).Get("signal")) == "15"
		}
	}
	if !terminated {
		t.Fatal("native normal LSP close did not deliver actual Source default SIGTERM", string(rawMain(frames)))
	}
	normalize := func(rows []protocol.JSONObject) []string {
		out := []string{}
		for _, r := range rows {
			if r.String("method") == "shutdown" || r.String("method") == "exit" || r.String("method") == "$/fixture/terminated" {
				continue
			}
			params := mainObj(r.Get("params"))
			if r.String("method") == "initialize" {
				var pid float64
				_ = json.Unmarshal(params.Get("processId"), &pid)
				if pid != float64(os.Getpid()) && pid != float64(cmd.Process.Pid) {
					t.Fatal("actual initialize PID", pid)
				}
				params.Set("processId", rawMain(0))
			}
			out = append(out, r.String("method")+":"+string(lspWireCanonicalJSON(rawMain(params))))
		}
		return out
	}
	if !reflect.DeepEqual(normalize(frames), normalize(original.Frames)) {
		t.Fatalf("actual LSP wire consumers\nGo=%s\nSource=%s", rawMain(frames), rawMain(original.Frames))
	}
}

func lspWireCanonicalJSON(value json.RawMessage) json.RawMessage {
	if len(value) > 0 && value[0] == '{' {
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(value, &fields)
		for k, v := range fields {
			fields[k] = lspWireCanonicalJSON(v)
		}
		return rawMain(fields)
	}
	if len(value) > 0 && value[0] == '[' {
		var values []json.RawMessage
		_ = json.Unmarshal(value, &values)
		for i, v := range values {
			values[i] = lspWireCanonicalJSON(v)
		}
		return rawMain(values)
	}
	return protocol.StringifyValidatedJavaScriptJSON(value)
}
