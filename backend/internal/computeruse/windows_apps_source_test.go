package computeruse

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestActualWindowsInstalledAppsHTTPMatchesOriginalSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source needs pinned Bun")
	}
	repository, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name           string
		python, helper bool
		code           int
		stdout         string
	}{
		{name: "cold"}, {name: "missing-helper", python: true}, {name: "missing-python", helper: true},
		{name: "unicode-order-extra-fields", python: true, helper: true, stdout: `{"ok":true,"result":[{"bundleId":"z.exe","displayName":"中😀","path":"C:\\fixture.exe","future":{"keep":true}},{"bundleId":"a.exe","displayName":"A","path":"C:\\a.exe"}]}`},
		{name: "failed-process", python: true, helper: true, code: 7, stdout: `{"ok":true,"result":[{}]}`},
		{name: "malformed", python: true, helper: true, stdout: `{"ok":true}` + " log"},
		{name: "missing-result", python: true, helper: true, stdout: `{"ok":true}`},
		{name: "null-result", python: true, helper: true, stdout: `{"ok":true,"result":null}`},
		{name: "false-ok", python: true, helper: true, stdout: `{"ok":false,"result":[{}]}`},
		{name: "truthy-ok", python: true, helper: true, stdout: `{"ok":"yes","result":{"future":42}}`},
		{name: "null-envelope", python: true, helper: true, stdout: `null`},
		{name: "JS-number-key-order-and-UTF16", python: true, helper: true, stdout: `{"ok":true,"result":{"9":9,"2":2,"number":9007199254740993,"unit":"\ud800","overflow":1e400}}`},
		{name: "original-trim-BOM", python: true, helper: true, stdout: "\ufeff " + `{"ok":true,"result":[{"bundleId":"bom.exe"}]}` + "\u00a0"},
	} {
		t.Run(c.name, func(t *testing.T) {
			files := map[string]string{}
			if c.python {
				files["venv/Scripts/python.exe"] = "owned-python"
			}
			if c.helper {
				files["win_helper.py"] = "owned-helper"
			}
			plan := []windowsCommandFixture{}
			if c.python && c.helper {
				plan = append(plan, windowsCommandFixture{Command: "$ROOT/config/.runtime/venv/Scripts/python.exe", Args: []string{"$ROOT/config/.runtime/win_helper.py", "list_installed_apps"}, Code: c.code, Stdout: c.stdout})
			}
			root := t.TempDir()
			encoded, _ := json.Marshal(map[string]any{"root": root, "repository": repository, "files": files, "plan": plan})
			fixture, _ := filepath.Abs("testdata/windows_apps.fixture.ts")
			cmd := exec.Command(bun, "--no-env-file", fixture)
			cmd.Dir = root
			cmd.Env = []string{"HOME=" + root, "USERPROFILE=" + root, "CLAUDE_CONFIG_DIR=" + filepath.Join(root, "config"), "TMPDIR=" + root, "PATH=/usr/bin:/bin", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
			cmd.Stdin = bytes.NewReader(encoded)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("actual Source apps: %v %s", err, output)
			}
			var expected struct {
				Result struct {
					Status int             `json:"status"`
					Body   json.RawMessage `json:"body"`
				} `json:"result"`
				Calls    [][]string        `json:"calls"`
				Files    map[string]string `json:"files"`
				Consumed int               `json:"consumed"`
			}
			if err = json.Unmarshal(output, &expected); err != nil {
				t.Fatalf("original output %v %s", err, output)
			}
			o := fixtureOptions(t)
			o.Platform = "win32"
			for name, body := range files {
				path := filepath.Join(o.ConfigDir, ".runtime", name)
				if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
					t.Fatal(e)
				}
				if e := os.WriteFile(path, []byte(body), 0700); e != nil {
					t.Fatal(e)
				}
			}
			calls := [][]string{}
			o.Run = func(_ context.Context, command string, args []string, _ []*os.File) (CommandResult, error) {
				row := append([]string{command}, args...)
				for i := range row {
					row[i] = strings.ReplaceAll(row[i], filepath.Dir(o.ConfigDir), "$ROOT")
				}
				calls = append(calls, row)
				if len(calls) > len(plan) {
					return CommandResult{Code: 1}, fmt.Errorf("unexpected command")
				}
				return CommandResult{Code: c.code, Stdout: c.stdout}, nil
			}
			s := fixtureService(t, o)
			status, body := request(t, s, "GET", "apps", "")
			var want, got any
			if e := json.Unmarshal(expected.Result.Body, &want); e != nil {
				t.Fatal(e)
			}
			if e := json.Unmarshal(body, &got); e != nil {
				t.Fatal(e)
			}
			var compact bytes.Buffer
			if e := json.Compact(&compact, body); e != nil {
				t.Fatal(e)
			}
			if !bytes.Equal(compact.Bytes(), expected.Result.Body) {
				t.Fatalf("HTTP JSON bytes differ: Go %s Source %s", compact.Bytes(), expected.Result.Body)
			}
			if status != expected.Result.Status || !reflect.DeepEqual(want, got) || !reflect.DeepEqual(calls, expected.Calls) || len(calls) != expected.Consumed {
				t.Fatalf("actual Go differs from actual Source: status %d/%d body %s/%s calls %v/%v", status, expected.Result.Status, body, expected.Result.Body, calls, expected.Calls)
			}
			if hashes := setupFileHashes(t, filepath.Join(o.ConfigDir, ".runtime")); !reflect.DeepEqual(hashes, expected.Files) {
				t.Fatalf("apps changed runtime: %v/%v", hashes, expected.Files)
			}
		})
	}
}
