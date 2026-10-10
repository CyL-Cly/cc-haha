package computeruse

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsSetupProducesOriginalRuntimeFilesAndStatus(t *testing.T) {
	o := fixtureOptions(t)
	o.Platform = "win32"
	calls := []string{}
	o.Run = func(_ context.Context, cmd string, args []string, _ []*os.File) (CommandResult, error) {
		calls = append(calls, cmd+" "+strings.Join(args, " "))
		if len(args) == 1 && args[0] == "--version" {
			return CommandResult{Stdout: "Python 3.12.7"}, nil
		}
		if cmd == "where" {
			return CommandResult{Stdout: "C:\\Python312\\python3.exe\r\n"}, nil
		}
		if len(args) > 1 && args[1] == "venv" {
			if e := os.MkdirAll(filepath.Join(args[2], "Scripts"), 0700); e != nil {
				t.Fatal(e)
			}
			for _, name := range []string{"python.exe", "pip.exe"} {
				if e := os.WriteFile(filepath.Join(args[2], "Scripts", name), []byte("owned-test"), 0700); e != nil {
					t.Fatal(e)
				}
			}
		}
		if len(args) > 1 && args[1] == "check_permissions" {
			return CommandResult{Stdout: `{"ok":true,"result":{"accessibility":true,"screenRecording":true}}`}, nil
		}
		return CommandResult{}, nil
	}
	service := fixtureService(t, o)
	status, body := request(t, service, http.MethodPost, "setup", "")
	if status != 200 {
		t.Fatalf("setup still missing: %d %s", status, body)
	}
	result, _ := decodeObject(body)
	if string(result["success"]) != "true" {
		t.Fatalf("setup failed: %s", body)
	}
	runtime := filepath.Join(o.ConfigDir, ".runtime")
	for _, name := range []string{"requirements.txt", "win_helper.py", "win_cursor_badge.py", "requirements.sha256", "venv-base-interpreter.txt"} {
		if _, e := os.ReadFile(filepath.Join(runtime, name)); e != nil {
			t.Fatal(name, e)
		}
	}
	var steps []struct {
		Name string `json:"name"`
	}
	if e := json.Unmarshal(result["steps"], &steps); e != nil {
		t.Fatal(e)
	}
	if len(steps) != 5 {
		t.Fatal(steps)
	}
	status, body = request(t, service, http.MethodGet, "status", "")
	if status != 200 {
		t.Fatalf("status still missing: %d %s", status, body)
	}
	value, _ := decodeObject(body)
	if string(value["engine"]) != `"windows-compat"` {
		t.Fatal(string(body))
	}
	venv, _ := decodeObject(value["venv"])
	deps, _ := decodeObject(value["dependencies"])
	if string(venv["created"]) != "true" || string(deps["installed"]) != "true" {
		t.Fatal(string(body))
	}
	first := len(calls)
	status, body = request(t, service, http.MethodPost, "setup", "")
	if status != 200 || !strings.Contains(string(body), "依赖已是最新") {
		t.Fatal(status, string(body))
	}
	for _, call := range calls[first:] {
		if strings.Contains(call, " pip ") {
			t.Fatal("unchanged setup installed twice", call)
		}
	}
}
