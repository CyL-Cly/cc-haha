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
	"github.com/NanmiCoder/cc-haha/backend/internal/lspruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func newNativeLSPLifecycleFixture(t *testing.T, interactive, simple, trusted bool, serverEnvironment func(nativePluginFixture) map[string]string) (nativePluginFixture, *MainPrint, string) {
	t.Helper()
	f := newNativePluginFixture(t)
	f.n.Environment["ANTHROPIC_API_KEY"] = "private-lsp-fake"
	f.n.Environment["USER_TYPE"] = "external"
	f.n.Environment["PATH"] = "/usr/bin:/bin"
	f.n.Environment["CLAUDE_CODE_DISABLE_AUTO_MEMORY"] = "1"
	if simple {
		f.n.Environment["CLAUDE_CODE_SIMPLE"] = "1"
	}
	peer, receipt := filepath.Join(f.plugin, "bin", "private-lsp-peer"), filepath.Join(f.home, "actual-lsp-frames.jsonl")
	data, err := os.ReadFile("../../testdata/lsp-file-notify-peer.py")
	if err != nil {
		t.Fatal(err)
	}
	f.write(t, peer, string(data))
	if err = os.Chmod(peer, 0700); err != nil {
		t.Fatal(err)
	}
	f.write(t, filepath.Join(f.plugin, ".claude-plugin", "plugin.json"), `{"name":"sample","version":"1.0.0"}`)
	config := protocol.NewJSONObject(fieldMain("command", peer), fieldMain("args", []string{receipt, f.home}), fieldMain("workspaceFolder", f.n.CWD), fieldMain("extensionToLanguage", map[string]string{".txt": "plaintext"}))
	if serverEnvironment != nil {
		config.Set("env", rawMain(serverEnvironment(f)))
	}
	f.write(t, filepath.Join(f.plugin, ".lsp.json"), string(rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "fixture", Value: rawMain(config)}))))
	installed, err := pluginregistry.NewInstalledRegistry(pluginregistry.InstalledOptions{PluginsDir: filepath.Join(f.config, "plugins"), Store: storage.New(storage.Options{})})
	if err != nil {
		t.Fatal(err)
	}
	if err = installed.Add(context.Background(), "sample@mkt", "user", f.plugin, mainObj(json.RawMessage(`{"name":"sample","version":"1.0.0"}`)), nil); err != nil {
		t.Fatal(err)
	}
	if interactive {
		seedNativeReleaseWarmFixture(t, f, "fixture")
	}
	if trusted {
		path := filepath.Join(f.config, ".claude.json")
		value := protocol.NewJSONObject()
		if data, err := os.ReadFile(path); err == nil {
			if err = value.UnmarshalJSON(data); err != nil {
				t.Fatal(err)
			}
		}
		value.Set("projects", rawMain(map[string]any{f.n.CWD: map[string]any{"hasTrustDialogAccepted": true}}))
		f.write(t, path, string(rawMain(value)))
	}
	disabled := false
	m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", Tools: []string{"Read,Write"}, MaxTurns: 8, PermissionMode: "bypassPermissions", Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Platform: runtime.GOOS, Version: "fixture", Interactive: interactive, Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
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

func TestNativeLSPActualStartupGatesLoadConfigurationWithoutSpawning(t *testing.T) {
	for _, tc := range []struct {
		name                                  string
		interactive, simple, trusted, enabled bool
	}{{name: "ordinary-headless", enabled: true}, {name: "interactive-before-trust", interactive: true}, {name: "interactive-after-trust", interactive: true, trusted: true, enabled: true}, {name: "simple-headless", simple: true}} {
		t.Run(tc.name, func(t *testing.T) {
			f, m, receipt := newNativeLSPLifecycleFixture(t, tc.interactive, tc.simple, tc.trusted, nil)
			if (m.nativeLSP != nil) != tc.enabled {
				t.Fatal("actual native startup manager gate", m.nativeLSP != nil, tc.enabled)
			}
			if tc.enabled {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if err := m.nativeLSP.Wait(ctx); err != nil {
					t.Fatal(err)
				}
				rows := m.nativeLSP.State()
				if len(rows) != 1 || rows[0].String("name") != "plugin:sample:fixture" || rows[0].String("state") != "stopped" {
					t.Fatal("actual installed-plugin configuration did not remain lazy", string(rawMain(rows)))
				}
			}
			if _, err := os.Stat(receipt); !os.IsNotExist(err) {
				t.Fatal("startup spawned an LSP peer", err)
			}
			before, err := os.ReadFile(filepath.Join(f.config, "settings.json"))
			if err != nil {
				t.Fatal(err)
			}
			m.BeginNativeLSPClose()
			if err = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				binding, err := m.Controller.BorrowNativeCopyBinding(m.Owner)
				if err != nil {
					return err
				}
				return m.initializeNativeLSPOnOwner(scope, binding)
			}); !errors.Is(err, cooperative.ErrClosed) {
				t.Fatal("closed lifetime admitted a late LSP startup", err)
			}
			m.Close()
			after, err := os.ReadFile(filepath.Join(f.config, "settings.json"))
			if err != nil || string(after) != string(before) {
				t.Fatal("LSP startup/close rewrote user settings", err)
			}
		})
	}
	var missing *MainPrint
	missing.BeginNativeLSPClose()
	if err := missing.closeNativeLSP(context.Background()); err != nil {
		t.Fatal("nil lifecycle close", err)
	}
}

func TestNativeLSPConcreteDetachedChildrenBorrowOnePeerAndReleaseOnlyTheirOwnLedger(t *testing.T) {
	f, parent, receipt := newNativeLSPLifecycleFixture(t, false, false, false, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := parent.nativeLSP.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.n.CWD, "shared-process.txt")
	f.write(t, path, "shared physical LSP file\n")
	if err := parent.nativeLSP.Open(path, quoteMain("shared physical LSP file\n")); err != nil {
		t.Fatal(err)
	}
	first, second := presetChildFixture(t, parent, ""), presetChildFixture(t, parent, "")
	if first.nativeLSP != parent.nativeLSP || second.nativeLSP != parent.nativeLSP || !first.nativeLSPLife.borrowed || !second.nativeLSPLife.borrowed {
		t.Fatal("actual detached factories created separate LSP managers")
	}
	firstBinding, secondBinding := first.nativeLSPLife.binding, second.nativeLSPLife.binding
	if !parent.nativeLSP.Owns(first.Owner, firstBinding) || !parent.nativeLSP.Owns(second.Owner, secondBinding) {
		t.Fatal("actual child native ledgers were not registered")
	}
	first.Close()
	if parent.nativeLSP.Owns(first.Owner, firstBinding) || !parent.nativeLSP.Owns(second.Owner, secondBinding) || !parent.nativeLSP.Owns(parent.Owner, parent.nativeLSPLife.binding) {
		t.Fatal("child close changed another process ledger")
	}
	if _, found, err := second.nativeLSP.Request(path, "$/fixture/barrier", nil); err != nil || !found {
		t.Fatal("first child close killed the live shared peer", found, err)
	}
	second.Close()
	if parent.nativeLSP.Owns(second.Owner, secondBinding) || !parent.nativeLSP.Enabled() {
		t.Fatal("second child close retained authority or closed the process singleton")
	}
	if _, found, err := parent.nativeLSP.Request(path, "$/fixture/barrier", nil); err != nil || !found {
		t.Fatal("child close killed the parent's original peer", found, err)
	}
	starts, opens := 0, 0
	var peerPID int
	for _, row := range nativeLSPPhysicalFrames(t, receipt) {
		var pid int
		if err := json.Unmarshal(row.Get("pid"), &pid); err != nil {
			t.Fatal(err)
		}
		if peerPID == 0 {
			peerPID = pid
		}
		if peerPID != pid {
			t.Fatal("shared children started another actual peer", peerPID, pid)
		}
		if row.String("method") == "initialize" {
			starts++
		}
		if row.String("method") == "textDocument/didOpen" {
			opens++
			params, _ := row.Object("params")
			document, _ := params.Object("textDocument")
			if document.String("uri") != lspruntime.URI(path) {
				t.Fatal("shared peer opened a different path")
			}
		}
		if row.String("method") == "shutdown" || row.String("method") == "exit" {
			t.Fatal("child close disposed the process singleton", string(rawMain(row)))
		}
	}
	if starts != 1 || opens != 1 {
		t.Fatal("children duplicated server initialization or open state", starts, opens)
	}
}

func nativeLSPPhysicalFrames(t *testing.T, path string) []protocol.JSONObject {
	t.Helper()
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	rows := []protocol.JSONObject{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var row protocol.JSONObject
		if err := row.UnmarshalJSON(scanner.Bytes()); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return rows
}
