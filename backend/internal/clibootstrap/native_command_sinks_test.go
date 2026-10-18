package clibootstrap

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/plugincli"
)

// Exercise the actual non-model factory, its public initialization caller and
// the original plugin handler. A standalone leaf Run must not acquire startup.
// Source's preAction attaches the debug sink before invoking its action.
func TestNativeCommandPreActionAttachesPhysicalDebugSinkBeforePluginHandler(t *testing.T) {
	f := newNativePluginFixture(t)
	debugFile := filepath.Join(f.config, "command-debug", "private.log")
	f.n.Environment["DEBUG"] = "1"
	f.n.Environment["NODE_ENV"] = "production"
	f.n.Environment["CLAUDE_CODE_DEBUG_LOGS_DIR"] = debugFile
	f.n.Environment["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"
	protected, err := os.ReadFile(filepath.Join(f.config, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewMainCommand(MainCommandOptions{
		Arguments: []string{"plugin", "marketplace", "list", "--json"}, Native: f.n,
		Platform: runtime.GOOS, PID: os.Getpid(), Version: "private-command-sinks",
		NonInteractive: true, Output: io.Discard, Error: io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if err := m.Run(ctx, func(*cooperative.Scope) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(debugFile); !os.IsNotExist(err) {
		t.Fatal("leaf Run acquired the separate CLI preAction sink", err)
	}
	if err := m.BeginNativeAPIPreconnect(ctx); err != nil {
		t.Fatal(err)
	}
	var result plugincli.Result
	if err := m.Run(ctx, func(scope *cooperative.Scope) error {
		data, err := os.ReadFile(debugFile)
		if err != nil || strings.Count(string(data), "[DEBUG] Error log sink initialized\n") != 1 {
			return fmt.Errorf("actual preAction did not attach its physical debug sink before action: %v %q", err, data)
		}
		result, err = m.PluginCommand(ctx, scope, plugincli.Command{Action: "marketplace-list", JSON: true})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || result.Stderr != "" || !strings.Contains(result.Stdout, `"name": "mkt"`) || strings.Contains(result.Stdout, "[DEBUG]") {
		t.Fatal("sink changed the actual plugin result", result)
	}
	m.Close()
	data, err := os.ReadFile(debugFile)
	if err != nil || strings.Count(string(data), "[DEBUG] Error log sink initialized\n") != 1 {
		t.Fatal("normal command close lost or duplicated the startup debug record", err, string(data))
	}
	after, err := os.ReadFile(filepath.Join(f.config, "settings.json"))
	if err != nil || string(after) != string(protected) {
		t.Fatal("preAction logging changed protected settings", err)
	}
}
