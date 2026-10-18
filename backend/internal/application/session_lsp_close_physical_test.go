//go:build !windows

package application

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualSessionCloseJoinsHeldLSPInitializeAndRequestBeforePendingASK(t *testing.T) {
	for _, heldMethod := range []string{"initialize", "textDocument/hover"} {
		t.Run(heldMethod, func(t *testing.T) {
			var models atomic.Int32
			var file string
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if models.Add(1) != 1 {
					t.Error("global close advanced a held LSP task to another model request")
					http.Error(w, "no inference after close", 500)
					return
				}
				response(w, "global-close-real-lsp", "LSP", map[string]any{"operation": "hover", "filePath": file, "line": 1, "character": 1}, "")
			}))
			defer model.Close()
			o := nativeSessionOptions(t, model.URL)
			o.Interactive, o.NonInteractivePermissions = true, true
			o.Arguments.Tools = []string{"LSP"}
			for _, environment := range []map[string]string{o.Environment, o.Native.Environment} {
				environment["ENABLE_LSP_TOOL"] = "1"
				environment["CLAUDE_CODE_DISABLE_AUTO_MEMORY"] = "1"
				environment["NO_PROXY"] = "127.0.0.1,localhost"
			}
			plugin := prepareTerminalPluginFixture(t, o)
			manifest := obj([]byte(`{"name":"sample","version":"1.0.0"}`))
			if err := os.WriteFile(filepath.Join(plugin, ".claude-plugin", "plugin.json"), raw(manifest), 0600); err != nil {
				t.Fatal(err)
			}
			privateRoot := filepath.Dir(o.Native.Home)
			receipt := filepath.Join(privateRoot, "actual-lsp-close-frames.jsonl")
			pipe := filepath.Join(privateRoot, "actual-lsp-release")
			if err := syscall.Mkfifo(pipe, 0600); err != nil {
				t.Fatal(err)
			}
			release, err := os.OpenFile(pipe, os.O_RDWR|syscall.O_NONBLOCK, 0600)
			if err != nil {
				t.Fatal(err)
			}
			defer release.Close()
			peer := filepath.Join(o.Native.Home, "private-lsp-peer")
			script, err := os.ReadFile("../../testdata/lsp-file-notify-peer.py")
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(peer, script, 0700); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(plugin, ".lsp.json"), raw(map[string]any{"fixture": map[string]any{"command": peer, "args": []string{receipt, privateRoot}, "workspaceFolder": o.WorkDir, "extensionToLanguage": map[string]string{".txt": "plaintext"}, "env": map[string]string{"CC_HAHA_LSP_HOLD_METHOD": heldMethod, "CC_HAHA_LSP_REQUEST_RELEASE": pipe}}}), 0600); err != nil {
				t.Fatal(err)
			}
			config := o.Environment["CLAUDE_CONFIG_DIR"]
			if err = os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"enabledPlugins":{"sample@mkt":true},"future":{"preserve":true}}`), 0600); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(config, ".claude.json"), raw(map[string]any{"hasCompletedOnboarding": true, "projects": map[string]any{o.WorkDir: map[string]any{"hasTrustDialogAccepted": true}}}), 0600); err != nil {
				t.Fatal(err)
			}
			seedTerminalReleaseWarmFixture(t, o)
			installed, err := pluginregistry.NewInstalledRegistry(pluginregistry.InstalledOptions{PluginsDir: filepath.Join(config, "plugins"), Store: storage.New(storage.Options{})})
			if err != nil {
				t.Fatal(err)
			}
			if err = installed.Add(context.Background(), "sample@mkt", "user", plugin, manifest, nil); err != nil {
				t.Fatal(err)
			}
			file = filepath.Join(o.WorkDir, "held-lsp.txt")
			if err = os.WriteFile(file, []byte("physical LSP input\n"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			s, err := NewSession(ctx, o)
			if err != nil {
				t.Fatal(err)
			}
			defer s.process.Close()
			for {
				ready := false
				if err := s.process.Owner.Run(ctx, func(scope *cooperative.Scope) error {
					var err error
					ready, err = s.process.Host.HasRegisteredTool(scope, "LSP")
					return err
				}); err != nil {
					t.Fatal(err)
				}
				if ready {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("actual interactive LSP catalog never became ready", context.Cause(ctx))
				case <-time.After(time.Millisecond):
				}
			}
			submitted := make(chan error, 1)
			go func() { submitted <- s.Submit(ctx, tui.Input{Text: "Hover the private file using LSP"}, nil) }()
			pid := 0
			for pid == 0 {
				stream, err := os.Open(receipt)
				if err == nil {
					scanner := bufio.NewScanner(stream)
					for scanner.Scan() {
						row := obj(scanner.Bytes())
						if row.String("method") == heldMethod {
							if err := json.Unmarshal(row.Get("pid"), &pid); err != nil {
								t.Fatal(err)
							}
						}
					}
					_ = stream.Close()
				}
				if pid != 0 {
					break
				}
				select {
				case err := <-submitted:
					t.Fatal("actual LSP task ended before entering the held protocol operation", err)
				case <-ctx.Done():
					t.Fatal("actual LSP protocol operation never entered", context.Cause(ctx))
				case <-time.After(time.Millisecond):
				}
			}
			closeCtx, closeCancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer closeCancel()
			closed := make(chan error, 1)
			go func() { closed <- s.Close(closeCtx) }()
			select {
			case err := <-closed:
				if err != nil {
					t.Fatal("actual global close", err)
				}
			case <-closeCtx.Done():
				// Release only after the failure is observed, so the disposable
				// fixture can drain without making the product assertion pass.
				_, _ = release.Write([]byte("1"))
				<-closed
				t.Fatal("Session.Close waited on a held LSP operation before canceling it")
			}
			select {
			case <-submitted:
			case <-ctx.Done():
				t.Fatal("global close left its actual ASK alive", context.Cause(ctx))
			}
			if models.Load() != 1 || !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
				t.Fatal("global close advanced the model or retained its LSP process", models.Load(), pid)
			}
		})
	}
}
