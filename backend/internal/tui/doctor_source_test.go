package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDoctorLocalJSXConfirmationContractAgainstOriginalReactConsumer(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	diagnostic := map[string]any{"installationType": "native", "version": "fixture", "installationPath": "/private/native/claude", "invokedBinary": "/private/native/claude", "configInstallMethod": "native", "autoUpdates": "enabled", "hasUpdatePermissions": nil, "multipleInstallations": []any{}, "warnings": []any{}, "ripgrepStatus": map[string]any{"working": true, "mode": "system", "systemPath": "/private/rg"}}
	fixture, _ := filepath.Abs("testdata/doctor.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"homeDir": home, "diagnostic": diagnostic, "actions": []string{"confirm:yes", "confirm:no", "unbound"}})
	command.Stdin = bytes.NewReader(input)
	out, e := command.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(out))
	}
	var rows []struct {
		Callbacks [][]json.RawMessage
		Context   map[string]string
		Text      string
	}
	if e = json.Unmarshal(out, &rows); e != nil {
		t.Fatal(e, string(out))
	}
	for index, key := range []string{"enter", "esc", "x"} {
		state := NewState(nil, 80, 24)
		state.Apply(Event{Kind: DoctorOpened, Text: "Diagnostics\nfixture"})
		state.HandleKey(Key{Name: key}, time.Now())
		closed := state.Doctor == nil
		if closed != (len(rows[index].Callbacks) > 0) {
			t.Fatal("Source Doctor confirmation differs", key, closed, rows[index].Callbacks)
		}
		if rows[index].Context["context"] != "Confirmation" {
			t.Fatal("Source confirmation context changed", rows[index].Context)
		}
		if closed {
			var text string
			var display map[string]string
			_ = json.Unmarshal(rows[index].Callbacks[0][0], &text)
			_ = json.Unmarshal(rows[index].Callbacks[0][1], &display)
			if text != state.Entries[len(state.Entries)-1].Text || !reflect.DeepEqual(display, map[string]string{"display": "system"}) {
				t.Fatal("Source dismiss result differs", text, display, state.Entries)
			}
		}
		if !strings.Contains(rows[index].Text, "Diagnostics") || !strings.Contains(rows[index].Text, "/private/native/claude") {
			t.Fatal("actual original Doctor did not render installation data", rows[index].Text)
		}
	}
}
