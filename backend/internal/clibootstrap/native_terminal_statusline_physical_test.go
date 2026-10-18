package clibootstrap

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func newNativeStatusLinePhysicalFixture(t *testing.T, settings protocol.JSONObject, trusted bool, prepare ...func(nativePluginFixture)) (nativePluginFixture, *MainPrint, string) {
	t.Helper()
	f := newNativePluginFixture(t)
	f.n.Environment["ANTHROPIC_API_KEY"] = "private-statusline-fake"
	f.n.Environment["USER_TYPE"] = "external"
	f.n.Environment["PATH"] = "/usr/bin:/bin"
	f.n.Environment["CLAUDE_CODE_DISABLE_AUTO_MEMORY"] = "1"
	f.n.Environment["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"
	receipt, script := filepath.Join(f.home, "actual-statusline-inputs.jsonl"), filepath.Join(f.home, "actual-statusline")
	f.write(t, script, "#!/bin/sh\nIFS= read -r input\nprintf '%s\\n' \"$input\" >> \"$HOME/actual-statusline-inputs.jsonl\"\nprintf '  \\033[32mACTUAL_NATIVE_STATUSLINE\\033[0m  \\n\\n  SECOND_LINE  \\n'\n")
	if err := os.Chmod(script, 0700); err != nil {
		t.Fatal(err)
	}
	if settings.Get("statusLine") == nil {
		settings = protocol.NewJSONObject(fieldMain("statusLine", protocol.NewJSONObject(fieldMain("type", "command"), fieldMain("command", "'"+strings.ReplaceAll(script, "'", "'\\''")+"'"), fieldMain("padding", 2))))
	}
	f.write(t, filepath.Join(f.config, "settings.json"), string(rawMain(settings)))
	seedNativeReleaseWarmFixture(t, f, "fixture")
	global := protocol.NewJSONObject(fieldMain("editorMode", "vim"))
	if data, err := os.ReadFile(filepath.Join(f.config, ".claude.json")); err == nil {
		if err = global.UnmarshalJSON(data); err != nil {
			t.Fatal(err)
		}
		global.Set("editorMode", quoteMain("vim"))
	}
	global.Set("projects", rawMain(map[string]any{f.n.CWD: map[string]any{"hasTrustDialogAccepted": trusted}}))
	f.write(t, filepath.Join(f.config, ".claude.json"), string(rawMain(global)))
	for _, configure := range prepare {
		configure(f)
	}
	disabled := false
	m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", MaxTurns: 8, PermissionMode: "bypassPermissions", Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Platform: runtime.GOOS, Version: "fixture", Interactive: true, Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	return f, m, receipt
}

func readStatusLinePhysicalRecords(t *testing.T, path string) []protocol.JSONObject {
	t.Helper()
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var rows []protocol.JSONObject
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var value protocol.JSONObject
		if err := value.UnmarshalJSON(scanner.Bytes()); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, value)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestNativeMountedStatusLineActualShellCallbackDataAndNativeGuards(t *testing.T) {
	f, m, receipt := newNativeStatusLinePhysicalFixture(t, protocol.JSONObject{}, true)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	state, err := m.TerminalStatusLineState(ctx)
	if err != nil || !state.Configured || !state.Trusted || state.Padding != 2 {
		t.Fatal("actual settings/trust projection", state, err)
	}
	cache := TerminalStatusLineCache{}
	result, err := m.RunTerminalStatusLine(ctx, "NORMAL", &cache, true)
	if err != nil || result.Text == nil || *result.Text != "\x1b[32mACTUAL_NATIVE_STATUSLINE\x1b[0m\nSECOND_LINE" {
		t.Fatal("actual native shell/trimmed ANSI output", result, err)
	}
	rows := readStatusLinePhysicalRecords(t, receipt)
	if len(rows) != 1 || !json.Valid(rawMain(rows[0])) {
		t.Fatal("actual callback stdin", string(rawMain(rows)))
	}
	input := rows[0]
	if input.String("session_id") != m.state.Snapshot().SessionID || input.String("cwd") != f.n.CWD || !strings.HasSuffix(input.String("transcript_path"), m.state.Snapshot().SessionID+".jsonl") || objMain(input.Get("workspace")).String("project_dir") != f.n.CWD || objMain(input.Get("vim")).String("mode") != "NORMAL" || input.Get("permission_mode") != nil || input.Get("agent_id") != nil {
		t.Fatal("actual canonical callback data", string(rawMain(input)))
	}
	for _, key := range []string{"cost", "context_window", "model", "output_style"} {
		if _, ok := input.Object(key); !ok {
			t.Fatal("missing actual callback object", key, string(rawMain(input)))
		}
	}
	override := "statusline-session-override"
	if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error { return m.Controller.ApplyReplModelOverride(scope, &override) }); err != nil {
		t.Fatal(err)
	}
	result, err = m.RunTerminalStatusLine(ctx, "NORMAL", &cache, false)
	if err != nil || objMain(result.Input.Get("model")).String("id") != override {
		t.Fatal("idle AppState session model override lost behind prior ASK model", result.Input, err)
	}
	_, foreign, _ := newNativeStatusLinePhysicalFixture(t, protocol.JSONObject{}, true)
	if _, err = foreign.StartTerminalStatusLine(ctx, "", &cache, false); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("foreign component cache admitted", err)
	}
	m.BeginNativeTerminalStatusLineClose()
	if _, err = m.StartTerminalStatusLine(ctx, "", &cache, false); !errors.Is(err, cooperative.ErrClosed) {
		t.Fatal("closed component admitted a late shell", err)
	}
	if len(readStatusLinePhysicalRecords(t, receipt)) != 2 {
		t.Fatal("late invocation executed a physical command")
	}
}

func TestNativeStatusLineTrustAndDisableSettingsPreventPhysicalCommand(t *testing.T) {
	for _, profile := range []string{"untrusted", "disabled"} {
		t.Run(profile, func(t *testing.T) {
			var settings protocol.JSONObject
			if profile == "disabled" {
				settings = protocol.NewJSONObject(fieldMain("disableAllHooks", true), fieldMain("statusLine", protocol.NewJSONObject(fieldMain("type", "command"), fieldMain("command", "printf forbidden"))))
			}
			_, m, receipt := newNativeStatusLinePhysicalFixture(t, settings, profile != "untrusted")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cache := TerminalStatusLineCache{}
			result, err := m.RunTerminalStatusLine(ctx, "", &cache, false)
			if err != nil || result.Text != nil || len(readStatusLinePhysicalRecords(t, receipt)) != 0 {
				t.Fatal("native hook ignored actual trust/disable producer", result, err)
			}
		})
	}
}
