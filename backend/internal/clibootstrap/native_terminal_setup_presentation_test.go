package clibootstrap

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualMainTerminalSetupRetainsStartupColorLibraryAndConsumesCurrentTheme(t *testing.T) {
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		http.Error(w, "local setup must not request a model", http.StatusInternalServerError)
	}))
	defer peer.Close()
	m := backgroundAgentMainWithOptions(t, peer.URL, func(native *NativeBoundary) {
		native.Environment["TERM_PROGRAM"] = "vscode"
		native.Environment["TERM_PROGRAM_VERSION"] = "1.72"
		native.Environment["FORCE_COLOR"] = "3"
		native.Environment["FORCE_HYPERLINK"] = "1"
	}, func(options *MainPrintOptions) { options.Interactive = true })
	ctx := context.Background()
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		for name, value := range map[string]string{"FORCE_COLOR": "0", "FORCE_HYPERLINK": "0", "TMUX": "late-mutable-process-env"} {
			if err := m.environment.SetProperty(quoteMain(name), quoteMain(value)); err != nil {
				return err
			}
		}
		output, err := m.nativeTerminalSetupCommand(ctx, scope, "")
		if err != nil || !strings.Contains(output, "\x1b[38;2;78;186;101mInstalled VSCode") || !strings.Contains(output, "\x1b]8;;file://") || !strings.Contains(output, "Application%20Support") {
			t.Fatal("actual cached Source module producer", err, output)
		}
		if err := m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
			next := current.Clone()
			next.Set("theme", quoteMain("light-ansi"))
			return &next, nil
		}); err != nil {
			return err
		}
		output, err = m.nativeTerminalSetupCommand(ctx, scope, "")
		if err != nil || !strings.Contains(output, "\x1b[33mFound existing VSCode") || !strings.Contains(output, "\x1b]8;;file://") {
			t.Fatal("same producer current theme and cached color support", err, output)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(m.options.Native.Home, "Library", "Application Support", "Code", "User", "keybindings.json")
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "workbench.action.terminal.sendSequence") || calls.Load() != 0 {
		t.Fatal("physical original setup consumer", err, string(data), calls.Load())
	}
}
