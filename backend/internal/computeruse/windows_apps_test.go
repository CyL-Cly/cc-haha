package computeruse

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsInstalledAppsColdReturnsOriginalEmptyListWithoutCommand(t *testing.T) {
	o := fixtureOptions(t)
	o.Platform = "win32"
	calls := 0
	o.Run = func(context.Context, string, []string, []*os.File) (CommandResult, error) {
		calls++
		return CommandResult{}, nil
	}
	s := fixtureService(t, o)
	status, body := request(t, s, "GET", "apps", "")
	if status != 200 || string(body) != "{\"apps\":[]}\n" || calls != 0 {
		t.Fatalf("original Windows cold apps must not be missing: %d %s calls=%d", status, body, calls)
	}
}
func TestWindowsInstalledAppsUsesExistingCompatibilityProcessAndPreservesFields(t *testing.T) {
	o := fixtureOptions(t)
	o.Platform = "win32"
	r := filepath.Join(o.ConfigDir, ".runtime")
	for _, file := range []string{"venv/Scripts/python.exe", "win_helper.py"} {
		p := filepath.Join(r, file)
		if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(p, []byte("owned fixture"), 0700); e != nil {
			t.Fatal(e)
		}
	}
	calls := 0
	o.Run = func(_ context.Context, cmd string, args []string, _ []*os.File) (CommandResult, error) {
		calls++
		if cmd != filepath.Join(r, "venv/Scripts/python.exe") || len(args) != 2 || args[0] != filepath.Join(r, "win_helper.py") || args[1] != "list_installed_apps" {
			t.Fatal(cmd, args)
		}
		return CommandResult{Stdout: `{"ok":true,"result":[{"bundleId":"owned.exe","displayName":"应用","path":"C:\\owned.exe","future":{"keep":true}}]}`}, nil
	}
	s := fixtureService(t, o)
	status, body := request(t, s, "GET", "apps", "")
	if status != 200 || calls != 1 {
		t.Fatal(status, string(body), calls)
	}
	value, _ := decodeObject(body)
	var apps []object
	if e := json.Unmarshal(value["apps"], &apps); e != nil {
		t.Fatal(e)
	}
	if len(apps) != 1 || apps[0]["future"] == nil {
		t.Fatal(string(body))
	}
}
