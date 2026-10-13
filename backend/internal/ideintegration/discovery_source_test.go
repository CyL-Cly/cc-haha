package ideintegration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type discoveryRow struct {
	CWD            string            `json:"cwd"`
	Platform       string            `json:"platform"`
	Terminal       string            `json:"terminal"`
	Env            map[string]string `json:"env"`
	Supported      bool              `json:"supported"`
	ParentPID      int               `json:"parentPID"`
	Running        []int             `json:"running"`
	Ancestors      []int             `json:"ancestors"`
	Responding     []int             `json:"responding"`
	Locks          []map[string]any  `json:"locks"`
	Cleanup        bool              `json:"cleanup"`
	IncludeInvalid bool              `json:"includeInvalid"`
	Operation      string            `json:"operation,omitempty"`
	Host           string            `json:"host,omitempty"`
}

func TestOriginalIDEWorkspacePortsStaleFilesAncestryAndFindBoundaries(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	lock := func(port int, cwd string, pid int) map[string]any {
		data, _ := json.Marshal(map[string]any{"workspaceFolders": []string{cwd}, "pid": pid, "ideName": "Owned IDE", "transport": "ws", "authToken": "fake-private-token"})
		return map[string]any{"path": fmt.Sprintf("/private/ide/%d.lock", port), "modified": float64(port), "data": string(data)}
	}
	base := discoveryRow{CWD: "/private/project/subdir", Platform: "darwin", Env: map[string]string{}, ParentPID: 10, Running: []int{20, 30}, Locks: []map[string]any{lock(1234, "/private/project", 20)}}
	rows := []discoveryRow{}
	add := func(change func(*discoveryRow)) {
		r := base
		r.Env = map[string]string{}
		r.Locks = append([]map[string]any{}, base.Locks...)
		change(&r)
		rows = append(rows, r)
	}
	add(func(*discoveryRow) {})
	add(func(r *discoveryRow) { r.Locks = []map[string]any{lock(1234, "/private/other", 20)} })
	add(func(r *discoveryRow) { r.Locks = []map[string]any{lock(1234, "/private/project-sibling", 20)} })
	add(func(r *discoveryRow) { r.CWD = "/private/project" })
	add(func(r *discoveryRow) {
		r.IncludeInvalid = true
		r.Locks = []map[string]any{lock(1234, "/private/other", 20)}
	})
	add(func(r *discoveryRow) {
		r.Env["CLAUDE_CODE_IDE_SKIP_VALID_CHECK"] = "true"
		r.Locks = []map[string]any{lock(1234, "/private/other", 20)}
	})
	add(func(r *discoveryRow) {
		r.Env["CLAUDE_CODE_SSE_PORT"] = "1234tail"
		r.Locks = []map[string]any{lock(1234, "/private/other", 20)}
	})
	add(func(r *discoveryRow) {
		r.Locks = append(r.Locks, lock(2345, "/private/project", 30))
		r.Env["CLAUDE_CODE_SSE_PORT"] = "1234"
	})
	add(func(r *discoveryRow) { r.Locks = append(r.Locks, lock(2345, "/private/project", 30)) })
	add(func(r *discoveryRow) { r.Supported = true; r.Terminal = "vscode"; r.Ancestors = []int{20} })
	add(func(r *discoveryRow) { r.Supported = true; r.Terminal = "vscode"; r.Ancestors = []int{99} })
	add(func(r *discoveryRow) { r.Supported = true; r.Terminal = "vscode"; r.ParentPID = 20 })
	add(func(r *discoveryRow) { r.Supported = true; r.Terminal = "vscode"; r.Running = nil })
	add(func(r *discoveryRow) {
		r.Supported = true
		r.Terminal = "vscode"
		r.Running = nil
		r.Env["CLAUDE_CODE_SSE_PORT"] = "1234"
	})
	add(func(r *discoveryRow) { r.Cleanup = true; r.Running = nil })
	add(func(r *discoveryRow) {
		r.Cleanup = true
		r.Locks = []map[string]any{lock(1234, "/private/project", 0)}
		r.Responding = []int{1234}
	})
	add(func(r *discoveryRow) { r.Cleanup = true; r.Locks = []map[string]any{lock(1234, "/private/project", 0)} })
	add(func(r *discoveryRow) {
		r.Cleanup = true
		r.Locks = []map[string]any{{"path": "/private/ide/1234.lock", "modified": 1, "readError": true, "data": ""}}
	})
	add(func(r *discoveryRow) {
		r.Locks = []map[string]any{{"path": "/private/ide/1234.lock", "modified": 1, "data": " /private/project \n"}}
	})
	add(func(r *discoveryRow) {
		r.Locks = []map[string]any{{"path": "/private/ide/1234.lock", "modified": 1, "data": `{"workspaceFolders":["/private/project"],"transport":"sse"}`}}
	})
	add(func(r *discoveryRow) {
		r.CWD = "/private/cafe\u0301/sub"
		r.Locks = []map[string]any{lock(1234, "/private/café", 20)}
	})
	add(func(r *discoveryRow) { r.Env["CLAUDE_CODE_IDE_HOST_OVERRIDE"] = "127.0.0.8" })
	add(func(r *discoveryRow) {
		r.Platform = "windows"
		r.CWD = `c:\project\sub`
		data, _ := json.Marshal(map[string]any{"workspaceFolders": []string{`C:\project`}, "pid": 20, "ideName": "Owned IDE", "transport": "ws"})
		r.Locks = []map[string]any{{"path": `C:\private\ide\1234.lock`, "modified": 1, "data": string(data)}}
	})
	add(func(r *discoveryRow) {
		r.Platform = "windows"
		r.CWD = `c:\PROJECT\sub`
		data, _ := json.Marshal(map[string]any{"workspaceFolders": []string{`C:\project`}, "pid": 20, "ideName": "Owned IDE", "transport": "ws"})
		r.Locks = []map[string]any{{"path": `C:\private\ide\1234.lock`, "modified": 1, "data": string(data)}}
	})
	add(func(r *discoveryRow) { r.Operation = "find" })
	add(func(r *discoveryRow) {
		r.Operation = "find"
		r.Locks = append(r.Locks, lock(2345, "/private/project", 30))
	})
	add(func(r *discoveryRow) { r.Operation = "find"; r.Locks = []map[string]any{} })

	for _, data := range []string{
		`{"workspaceFolders":["/private/project"],"ideName":"","authToken":""}`,
		`{"workspaceFolders":["/private/project"],"ideName":null,"authToken":null}`,
		`{"workspaceFolders":["/private/project"],"ideName":"raw\ud800","authToken":"a\udc00"}`,
		`{"workspaceFolders":["/private/project",42],"ideName":17}`,
		`{"workspaceFolders":[42,"/private/project"],"ideName":"bad"}`,
		`{"workspaceFolders":[null,false,0,"","/private/project"],"pid":"20"}`,
		`{"workspaceFolders":{},"ideName":"bypassed"}`,
		`{"workspaceFolders":[42],"ideName":"bypassed"}`,
		`null`, `42`, `[]`,
	} {
		add(func(r *discoveryRow) {
			r.Locks = []map[string]any{{"path": "/private/ide/1234.lock", "modified": 1, "data": data}}
		})
		add(func(r *discoveryRow) {
			r.Cleanup = true
			r.Locks = []map[string]any{{"path": "/private/ide/1234.lock", "modified": 1, "data": data}}
			r.Responding = []int{1234}
			r.Env["CLAUDE_CODE_IDE_SKIP_VALID_CHECK"] = "true"
		})
	}
	add(func(r *discoveryRow) {
		r.Env["CLAUDE_CODE_IDE_SKIP_VALID_CHECK"] = "true"
		r.Locks = []map[string]any{{"path": "/private/ide/no-number.lock", "modified": 1, "data": `{"workspaceFolders":[]}`}}
	})
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-ide-discovery.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_IDE_DISCOVERY_FIXTURE=1", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"homeDir": home, "rows": rows})
	command.Stdin = bytes.NewReader(data)
	out, e := command.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(out))
	}
	var source struct {
		Rows []struct {
			Result        json.RawMessage
			Removed       []string
			AncestryCalls int
			Elapsed       int
		}
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.Rows) != len(rows) {
		t.Fatal(e, string(out))
	}
	for i, r := range rows {
		t.Run(fmt.Sprintf("row-%02d", i), func(t *testing.T) {
			removed := []string{}
			ancestry := 0
			elapsed := 0
			contains := func(items []int, value int) bool {
				for _, item := range items {
					if item == value {
						return true
					}
				}
				return false
			}
			service := New(Options{CWD: r.CWD, Platform: r.Platform, Terminal: r.Terminal, Environment: r.Env, ParentPID: r.ParentPID, ProcessRunning: func(pid int) bool { return contains(r.Running, pid) }, Probe: func(_ context.Context, _ string, port float64) bool { return contains(r.Responding, int(port)) }, Ancestors: func(context.Context, int, int) ([]int, error) { ancestry++; return r.Ancestors, nil }, Remove: func(path string) error { removed = append(removed, path); return nil }, ReadLockfiles: func(context.Context) ([]Lockfile, error) {
				locks := []Lockfile{}
				for _, row := range r.Locks {
					path, _ := row["path"].(string)
					if strings.Contains(strings.Join(removed, "|"), path) {
						continue
					}
					text, _ := row["data"].(string)
					modified, _ := row["modified"].(float64)
					var err error
					if row["readError"] == true {
						err = errors.New("controlled read failure")
					}
					locks = append(locks, Lockfile{Path: path, Data: []byte(text), Modified: modified, ReadError: err})
				}
				return locks, nil
			}, Now: func() time.Time { return time.Unix(0, int64(elapsed)*int64(time.Millisecond)) }, Sleep: func(_ context.Context, d time.Duration) error { elapsed += int(d / time.Millisecond); return nil }})
			var result any
			if r.Operation == "find" {
				result, e = service.Find(ctx)
			} else {
				if r.Cleanup {
					e = service.Cleanup(ctx)
				}
				result, e = service.Detect(ctx, r.IncludeInvalid)
			}
			if e != nil {
				t.Fatal(e)
			}
			want := source.Rows[i]
			actual, _ := json.Marshal(result)
			var a, b any
			_ = json.Unmarshal(actual, &a)
			_ = json.Unmarshal(want.Result, &b)
			if !reflect.DeepEqual(a, b) || !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(actual), protocol.StringifyValidatedJavaScriptJSON(want.Result)) || !reflect.DeepEqual(removed, want.Removed) || ancestry != want.AncestryCalls || elapsed != want.Elapsed {
				t.Fatalf("Source row%d result%s removed%v ancestors%d elapsed%d; Go %s removed%v ancestors%d elapsed%d", i, want.Result, want.Removed, want.AncestryCalls, want.Elapsed, actual, removed, ancestry, elapsed)
			}
		})
	}
}
