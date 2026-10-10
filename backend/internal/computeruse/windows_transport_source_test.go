package computeruse

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func physicalWindowsOptions(t *testing.T) Options {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable/OS-primitives fixture; real Windows execution remains separate")
	}
	o := fixtureOptions(t)
	o.Platform = "win32"
	python := os.Getenv("CC_HAHA_FIXTURE_PYTHON")
	if python == "" {
		t.Skip("physical Python fixture needs explicit interpreter")
	}
	primitives, err := os.ReadFile("testdata/windows_os_primitives.py")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(o.WorkDir, "sitecustomize.py"), primitives, 0600); err != nil {
		t.Fatal(err)
	}
	o.Environment["PYTHONPATH"] = o.WorkDir + string(os.PathListSeparator) + os.Getenv("CC_HAHA_FIXTURE_SITE_PACKAGES")
	o.Environment["PYTHONDONTWRITEBYTECODE"] = "1"
	root := filepath.Join(o.ConfigDir, ".runtime")
	if err = os.MkdirAll(filepath.Join(root, "venv/Scripts"), 0700); err != nil {
		t.Fatal(err)
	}
	wrapper := "#!/bin/sh\nexec '" + python + "' \"$@\"\n"
	for name, body := range map[string][]byte{"venv/Scripts/python.exe": []byte(wrapper), "venv/Scripts/pip.exe": {}, "requirements.sha256": []byte(requirementsDigest(windowsRequirements) + "\n"), "requirements.txt": windowsRequirements, "win_helper.py": windowsHelper, "win_cursor_badge.py": windowsCursorBadge} {
		if err = os.WriteFile(filepath.Join(root, name), body, 0700); err != nil {
			t.Fatal(err)
		}
	}
	return o
}
func TestActualWindowsBridgePhysicalPythonMatchesOriginalSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source needs pinned Bun")
	}
	rows := []map[string]any{{"command": "check_permissions", "payload": map[string]any{}}, {"command": "list_displays", "payload": map[string]any{}}, {"command": "screenshot", "payload": map[string]any{"targetWidth": 4, "targetHeight": 3}}, {"command": "not_a_command", "payload": map[string]any{}}, {"command": "get_display_size", "payload": map[string]any{"displayId": 33}}}
	src := physicalWindowsOptions(t)
	repo, _ := filepath.Abs("../../..")
	fixture, _ := filepath.Abs("testdata/windows_transport.fixture.ts")
	input, _ := json.Marshal(map[string]any{"root": filepath.Dir(src.ConfigDir), "repository": repo, "rows": rows})
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = src.WorkDir
	for k, v := range src.Environment {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Source physical client: %v %s", err, out)
	}
	var oracle struct {
		Results []map[string]any
		Tag     string
	}
	if err = json.Unmarshal(out, &oracle); err != nil {
		t.Fatalf("Source: %s %v", out, err)
	}
	if len(oracle.Results) != 5 || oracle.Results[0]["error"] != nil {
		t.Fatalf("original physical helper setup failed: %v", oracle.Results)
	}
	if oracle.Tag == "" {
		t.Fatal("Source missing input tag")
	}
	t.Logf("Source physical helper completed %d rows", len(oracle.Results))
	o := physicalWindowsOptions(t)
	s := fixtureService(t, o)
	if err = s.UpdateConfig(context.Background(), json.RawMessage(`{"enabled":true}`)); err != nil {
		t.Fatal(err)
	}
	actual := []map[string]any{}
	for _, row := range rows {
		p, _ := json.Marshal(row["payload"])
		r, e := s.Call(context.Background(), "actual-session", row["command"].(string), p)
		if e != nil {
			actual = append(actual, map[string]any{"error": e.Error()})
		} else {
			var value any
			if e = json.Unmarshal(r, &value); e != nil {
				t.Fatal(e)
			}
			actual = append(actual, map[string]any{"result": value})
		}
	}
	if !reflect.DeepEqual(oracle.Results, actual) {
		t.Fatalf("physical originalSource=%v Go=%v", oracle.Results, actual)
	}
}

func windowsJournal(t *testing.T, path string) []map[string]any {
	t.Helper()
	body, _ := os.ReadFile(path)
	out := []map[string]any{}
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		if line == "" {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		out = append(out, row)
	}
	return out
}
func waitWindowsEvent(t *testing.T, path, event string) map[string]any {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		for _, r := range windowsJournal(t, path) {
			if r["event"] == event {
				return r
			}
		}
		select {
		case <-deadline.C:
			t.Fatalf("physical helper never emitted %s", event)
		case <-time.After(time.Millisecond):
		}
	}
}
func windowsPIDStopped(t *testing.T, o Options, pid int) {
	t.Helper()
	cmd := exec.Command("/bin/ps", "-o", "stat=", "-p", strconv.Itoa(pid))
	cmd.Env = []string{"HOME=" + o.HomeDir, "PATH=/usr/bin:/bin"}
	state, _ := cmd.Output()
	if trim(string(state)) != "" && !strings.HasPrefix(trim(string(state)), "Z") {
		t.Fatalf("physical child %d still running: %s", pid, state)
	}
}
func assertWindowsBadgeOrder(t *testing.T, rows []map[string]any) {
	t.Helper()
	hook := -1
	activities := 0
	tag := ""
	for i, r := range rows {
		if r["event"] == "hook" {
			hook = i
		}
		if r["event"] == "activity" {
			activities++
		}
		if r["event"] == "launch" {
			if value := r["tag"].(string); value == "" {
				t.Fatal("missing input tag")
			} else if tag == "" {
				tag = value
			} else if tag != value {
				t.Fatal("tag changed inside same Source/Go client")
			}
			argv := r["argv"].([]any)
			if strings.HasSuffix(argv[0].(string), "win_helper.py") && len(argv) > 1 && argv[1] == "get_app_state" && hook < 0 {
				t.Fatalf("helper launched before physical badge hook: %d", i)
			}
		}
	}
	if hook < 0 || activities != 2 {
		t.Fatalf("badge FIFO lifecycle missing hook/activity: %v", rows)
	}
}
func TestActualWindowsBridgeBadgeREADYActivityAndEndTurnMatchOriginalSource(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("isolated host OS primitive fixture is POSIX; Windows execution evidence separate")
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source needs pinned Bun")
	}
	rows := []map[string]any{{"command": "get_app_state", "payload": map[string]any{"pid": 42}}, {"command": "check_permissions", "payload": map[string]any{}}, {"command": "click", "payload": map[string]any{"x": 1000, "y": 1000, "pid": 42}}}
	src := physicalWindowsOptions(t)
	journal := filepath.Join(src.HomeDir, "events.jsonl")
	src.Environment["CC_HAHA_FIXTURE_JOURNAL"] = journal
	repo, _ := filepath.Abs("../../..")
	fixture, _ := filepath.Abs("testdata/windows_transport.fixture.ts")
	input, _ := json.Marshal(map[string]any{"root": filepath.Dir(src.ConfigDir), "repository": repo, "rows": rows, "routed": true, "endTurn": true})
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = src.WorkDir
	for k, v := range src.Environment {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Source badge process: %v %s", err, out)
	}
	var oracle struct{ Results []map[string]any }
	if err = json.Unmarshal(out, &oracle); err != nil {
		t.Fatalf("Source %s %v", out, err)
	}
	expectedJournal := windowsJournal(t, journal)
	assertWindowsBadgeOrder(t, expectedJournal)
	for _, r := range expectedJournal {
		if r["event"] == "launch" {
			windowsPIDStopped(t, src, int(r["pid"].(float64)))
		}
	}
	o := physicalWindowsOptions(t)
	journal = filepath.Join(o.HomeDir, "events.jsonl")
	o.Environment["CC_HAHA_FIXTURE_JOURNAL"] = journal
	s := fixtureService(t, o)
	if err = s.UpdateConfig(context.Background(), json.RawMessage(`{"enabled":true}`)); err != nil {
		t.Fatal(err)
	}
	actual := []map[string]any{}
	for _, r := range rows {
		p, _ := json.Marshal(r["payload"])
		result, e := s.Call(context.Background(), "badge-session", r["command"].(string), p)
		if e != nil {
			actual = append(actual, map[string]any{"error": e.Error()})
		} else {
			var value any
			json.Unmarshal(result, &value)
			actual = append(actual, map[string]any{"result": value})
		}
	}
	// Observe the actual reader, rather than timing-based assumptions about a pipe write.
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		n := 0
		for _, r := range windowsJournal(t, journal) {
			if r["event"] == "activity" {
				n++
			}
		}
		if n == 2 {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("badge failed to consume real activity FIFO")
		case <-time.After(time.Millisecond):
		}
	}
	if err = s.EndTurn(context.Background(), "badge-session"); err != nil {
		t.Fatal(err)
	}
	gotJournal := windowsJournal(t, journal)
	assertWindowsBadgeOrder(t, gotJournal)
	for _, r := range gotJournal {
		if r["event"] == "launch" {
			windowsPIDStopped(t, o, int(r["pid"].(float64)))
		}
	}
	if !reflect.DeepEqual(oracle.Results, actual) {
		t.Fatalf("Source=%v Go=%v", oracle.Results, actual)
	}
	old := s.windowsClients["badge-session"]
	if err = s.CloseSession(context.Background(), "badge-session"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Call(context.Background(), "badge-session", "check_permissions", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	fresh := s.windowsClients["badge-session"]
	if fresh == old || fresh.tag == old.tag {
		t.Fatal("retired same SID reused native client/tag")
	}
}
func TestWindowsBridgePhysicalHeldHelperAbortCloseAndNextSession(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("physical POSIX barrier fixture; Windows compile is separate")
	}
	for _, mode := range []string{"abort", "close-session", "close-service"} {
		t.Run(mode, func(t *testing.T) {
			o := physicalWindowsOptions(t)
			journal := filepath.Join(o.HomeDir, "events.jsonl")
			barrier := filepath.Join(o.WorkDir, "helper.fifo")
			mk := exec.Command("/usr/bin/mkfifo", barrier)
			mk.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + o.HomeDir}
			if body, err := mk.CombinedOutput(); err != nil {
				t.Fatalf("mkfifo %v %s", err, body)
			}
			o.Environment["CC_HAHA_FIXTURE_JOURNAL"] = journal
			o.Environment["CC_HAHA_FIXTURE_HOLD"] = "check_permissions"
			o.Environment["CC_HAHA_FIXTURE_BARRIER"] = barrier
			s := fixtureService(t, o)
			if err := s.UpdateConfig(context.Background(), json.RawMessage(`{"enabled":true}`)); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, e := s.Call(ctx, "held-session", "check_permissions", json.RawMessage(`{}`)); done <- e }()
			held := waitWindowsEvent(t, journal, "held")
			pid := int(held["pid"].(float64))
			if !processAlive(pid) {
				t.Fatal("helper never alive at held physical I/O")
			}
			closeCtx, cancelClose := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancelClose()
			var err error
			switch mode {
			case "abort":
				cancel()
			case "close-session":
				err = s.CloseSession(closeCtx, "held-session")
			case "close-service":
				err = s.Close(closeCtx)
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case e := <-done:
				if e == nil {
					t.Fatal("cancelled physical helper published success")
				}
			case <-closeCtx.Done():
				t.Fatal("native child did not join")
			}
			windowsPIDStopped(t, o, pid)
			if mode == "close-service" {
				if _, e := s.Call(context.Background(), "later", "list_displays", json.RawMessage(`{}`)); e == nil {
					t.Fatal("closed service admitted command")
				}
			} else {
				if _, e := s.Call(context.Background(), "held-session", "list_displays", json.RawMessage(`{}`)); e != nil {
					t.Fatal("next admitted request", e)
				}
			}
			t.Logf("%s actual held PID=%d joined; next request guarded", mode, pid)
		})
	}
}

func physicalInstallerOptions(t *testing.T) (Options, string, string) {
	t.Helper()
	o := physicalWindowsOptions(t)
	root := filepath.Join(o.ConfigDir, ".runtime")
	if err := os.RemoveAll(filepath.Join(root, "venv")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "requirements.sha256")); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile("testdata/windows_python_installer.py")
	if err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(o.WorkDir, "offline-python")
	script := append([]byte("#!"+os.Getenv("CC_HAHA_FIXTURE_PYTHON")+"\n"), body...)
	if err = os.WriteFile(launcher, script, 0700); err != nil {
		t.Fatal(err)
	}
	fail := filepath.Join(o.WorkDir, "fail-upgrade")
	if err = os.WriteFile(fail, []byte("owned failure"), 0600); err != nil {
		t.Fatal(err)
	}
	o.Environment["CC_HAHA_FIXTURE_JOURNAL"] = filepath.Join(o.HomeDir, "install.jsonl")
	o.Environment["CC_HAHA_FIXTURE_FAIL_UPGRADE"] = fail
	o.Environment["CC_HAHA_FIXTURE_PYTHON"] = os.Getenv("CC_HAHA_FIXTURE_PYTHON")
	return o, launcher, fail
}
func TestActualWindowsBridgeBootstrapUpgradeFailureRetryAndAssetsMatchOriginalSource(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("physical POSIX installer fixture; Windows native execution separate")
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source needs pinned Bun")
	}
	src, launcher, fail := physicalInstallerOptions(t)
	if err := os.MkdirAll(filepath.Join(src.ConfigDir, "cc-haha"), 0700); err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(map[string]any{"enabled": true, "pythonPath": launcher})
	if err := os.WriteFile(filepath.Join(src.ConfigDir, "cc-haha/computer-use-config.json"), config, 0600); err != nil {
		t.Fatal(err)
	}
	repo, _ := filepath.Abs("../../..")
	fixture, _ := filepath.Abs("testdata/windows_transport.fixture.ts")
	rows := []map[string]any{{"command": "check_permissions", "payload": map[string]any{}}, {"command": "check_permissions", "payload": map[string]any{}}}
	input, _ := json.Marshal(map[string]any{"root": filepath.Dir(src.ConfigDir), "repository": repo, "rows": rows, "retryAfterFailure": fail})
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = src.WorkDir
	for k, v := range src.Environment {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Source installer %v %s", err, out)
	}
	var oracle struct{ Results []map[string]any }
	if err = json.Unmarshal(out, &oracle); err != nil {
		t.Fatalf("Source %s %v", out, err)
	}
	if oracle.Results[0]["error"] == nil || oracle.Results[1]["error"] != nil {
		t.Fatalf("Source fixture mismatch %v", oracle.Results)
	}
	o, launcher, fail := physicalInstallerOptions(t)
	s := fixtureService(t, o)
	config, _ = json.Marshal(map[string]any{"enabled": true, "pythonPath": launcher})
	if err = s.UpdateConfig(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	_, err = s.Call(context.Background(), "install-session", "check_permissions", json.RawMessage(`{}`))
	if err == nil || err.Error() != oracle.Results[0]["error"] {
		t.Fatalf("upgrade refusal Source=%v Go=%v", oracle.Results[0], err)
	}
	if exists(filepath.Join(s.windowsRuntimeRoot(), "requirements.sha256")) {
		t.Fatal("failed install persisted success stamp")
	}
	if err = os.Remove(fail); err != nil {
		t.Fatal(err)
	}
	result, err := s.Call(context.Background(), "install-session", "check_permissions", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var got any
	json.Unmarshal(result, &got)
	if !reflect.DeepEqual(got, oracle.Results[1]["result"]) {
		t.Fatalf("retry result %s", result)
	}
	normalize := func(rows []map[string]any, root string) []string {
		out := []string{}
		for _, r := range rows {
			if r["event"] == "installer" {
				body, _ := json.Marshal(r["args"])
				out = append(out, strings.ReplaceAll(string(body), root, "$ROOT"))
			}
		}
		return out
	}
	expected := normalize(windowsJournal(t, src.Environment["CC_HAHA_FIXTURE_JOURNAL"]), filepath.Dir(src.ConfigDir))
	actual := normalize(windowsJournal(t, o.Environment["CC_HAHA_FIXTURE_JOURNAL"]), filepath.Dir(o.ConfigDir))
	if !reflect.DeepEqual(expected, actual) {
		t.Fatalf("Source install/retry ledger=%v Go=%v", expected, actual)
	}
	sourceFiles := setupFileHashes(t, filepath.Join(src.ConfigDir, ".runtime"))
	goFiles := setupFileHashes(t, s.windowsRuntimeRoot())
	for _, name := range []string{"requirements.txt", "win_helper.py", "win_cursor_badge.py", "requirements.sha256"} {
		if sourceFiles[name] != goFiles[name] {
			t.Fatalf("bootstrap asset/stamp difference %s", name)
		}
	}
}

func TestActualWindowsBridgePhysicalEnvelopeMatrixMatchesOriginalSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source needs pinned Bun")
	}
	matrix := []CommandResult{{Stdout: `{"ok":true,"result":{"9":9,"2":2,"n":9007199254740993,"unit":"\ud800"}}`}, {Code: 7, Stdout: `{"ok":true,"result":true}`}, {Stdout: `{"ok":false,"error":{"code":"user_interference","message":"用户😀"}}`}, {Stdout: `{"ok":false,"error":{"code":"runtime_error","message":"runtime"}}`}, {Stdout: `{"ok":false}`}, {Stdout: `null`}, {Stdout: `[]`}, {Stdout: `false`}, {Stdout: `"text"`}, {Stdout: `{"ok":"yes","result":null}`}, {Stdout: `{"ok":true}`}, {Code: 7, Stderr: "stderr\n"}, {Code: 7}, {Stdout: "not-json", Stderr: "parse priority\n"}, {Stdout: "not-json"}, {Stdout: "\ufeff{\"ok\":true,\"result\":true}"}, {Stdout: `{"ok":false,"error":{"code":7,"message":{"future":true}}}`}, {Code: 7, Stderr: "bare-cr\r"}, {Stdout: "invalid\r"}, {Code: 7, Stderr: "two-lines\n\n"}}
	fixture := func(o Options) {
		files := map[string]map[string]any{}
		for i, r := range matrix {
			files[strconv.Itoa(i)] = map[string]any{"stdout": r.Stdout, "stderr": r.Stderr, "code": r.Code}
		}
		body, _ := json.Marshal(files)
		path := filepath.Join(o.WorkDir, "envelopes.json")
		if e := os.WriteFile(path, body, 0600); e != nil {
			t.Fatal(e)
		}
		o.Environment["CC_HAHA_FIXTURE_ENVELOPE"] = path
	}
	rows := []map[string]any{}
	for i := range matrix {
		rows = append(rows, map[string]any{"command": strconv.Itoa(i), "payload": map[string]any{"unit": "UTF8😀"}})
	}
	src := physicalWindowsOptions(t)
	fixture(src)
	repo, _ := filepath.Abs("../../..")
	file, _ := filepath.Abs("testdata/windows_transport.fixture.ts")
	input, _ := json.Marshal(map[string]any{"root": filepath.Dir(src.ConfigDir), "repository": repo, "rows": rows})
	cmd := exec.Command(bun, "--no-env-file", file)
	cmd.Dir = src.WorkDir
	for k, v := range src.Environment {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdin = bytes.NewReader(input)
	body, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source envelope %v %s", e, body)
	}
	var oracle struct{ Results []json.RawMessage }
	if e = json.Unmarshal(body, &oracle); e != nil {
		t.Fatalf("Source %s %v", body, e)
	}
	o := physicalWindowsOptions(t)
	fixture(o)
	s := fixtureService(t, o)
	if e = s.UpdateConfig(context.Background(), json.RawMessage(`{"enabled":true}`)); e != nil {
		t.Fatal(e)
	}
	for i := range matrix {
		value, e := s.Call(context.Background(), "wire-session", strconv.Itoa(i), json.RawMessage(`{"unit":"UTF8😀"}`))
		fields := []protocol.JSONField{}
		if e != nil {
			fields = append(fields, protocol.JSONField{Name: "error", Value: encode(e.Error())})
		} else if len(value) != 0 {
			fields = append(fields, protocol.JSONField{Name: "result", Value: value})
		}
		got, _ := protocol.NewJSONObject(fields...).MarshalJSON()
		want, e := protocol.ParseAndStringifyJavaScriptJSON(oracle.Results[i])
		if e != nil {
			t.Fatal(e)
		}
		if string(got) != string(want) {
			t.Fatalf("wire row %d Source=%s Go=%s", i, want, got)
		}
	}
}
