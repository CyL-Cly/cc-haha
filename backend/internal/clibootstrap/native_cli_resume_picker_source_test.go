package clibootstrap

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
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestOriginalCLIResumePickerPhysicalLiteMetadataPRFiltersPaginationAndWorktreeDedup(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	f := newNativePluginFixture(t)
	f.n.Environment["PATH"] = "/usr/bin:/bin"
	directory := filepath.Join(f.config, "projects", projectpath.Sanitize(f.n.CWD))
	if e := os.MkdirAll(directory, 0700); e != nil {
		t.Fatal(e)
	}
	bodies := []string{
		`{"type":"user","message":{"content":"real first\n中文"},"cwd":` + string(quoteMain(f.n.CWD)) + `}\n{"type":"pr-link","prNumber":32,"prUrl":"https://github.com/a/b/pull/32","prRepository":"a/b"}`,
		`{"type":"user","message":{"content":[{"type":"text","text":"<ide_opened_file>ignored</ide_opened_file>"},{"type":"text","text":"VS_CODE_VISIBLE"}]}}\n{"customTitle":"Owned title","lastPrompt":"newer physical prompt","prNumber":"43tail"}`,
		`{"type":"user","isSidechain":true,"message":{"content":"sidechain"}}\n{"prNumber":32}`,
		`{"type":"user","teamName":"private-team","message":{"content":"team"}}\n{"prNumber":32}`,
		`{"type":"user","message":{"content":"<command-name>/settings</command-name><command-args>x</command-args>"}}\n{"prNumber":0}`,
		`{"type":"user","message":{"content":"<command-name>/owned-custom</command-name><command-args>custom args</command-args>"}}\n{"prNumber":-4}`,
		`{"type":"user","message":{"content":"<bash-input>echo private</bash-input>"}}\n{"customTitle":"","aiTitle":"MUST_NOT_OVERRIDE_EMPTY"}`,
		`{"type":"user","message":{"content":"[Request interrupted by user]"}}`,
		`{"type":"metadata"}`,
		`{"type":"user","message":{"content":"raw\ud800 preserved"}}\n{"customTitle":"title\udc00"}`,
	}
	originals := map[string][]byte{}
	for i, body := range bodies {
		sid := fmt.Sprintf("10000000-0000-4000-8000-%012d", i+1)
		path := filepath.Join(directory, sid+".jsonl")
		data := []byte(strings.ReplaceAll(body, `}\n{`, "}\n{") + "\n")
		if e := os.WriteFile(path, data, 0600); e != nil {
			t.Fatal(e)
		}
		stamp := time.Unix(1700000000+int64(i), 0)
		if e := os.Chtimes(path, stamp, stamp); e != nil {
			t.Fatal(e)
		}
		originals[path] = data
	}
	rows := []map[string]any{}
	for _, filter := range []any{nil, true, false, "32", "32suffix", "https://github.com/a/b/pull/43", "invalid", float64(-4), "0", "https://github.com/a/b/pull/0"} {
		rows = append(rows, map[string]any{"cwd": f.n.CWD, "worktrees": []string{}, "filter": filter, "count": 50})
	}
	rows = append(rows, map[string]any{"cwd": f.n.CWD, "worktrees": []string{}, "count": 2})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-cli-pr-picker.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = f.home
	cmd.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_CLI_PR_PICKER_FIXTURE=1", "PATH=/usr/bin:/bin", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "NODE_ENV=production", "ANTHROPIC_API_KEY=private-readonly-pr-picker-fake-key"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"homeDir": f.home, "rows": rows}))
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(out))
	}
	var source struct {
		Rows []struct {
			Logs             []protocol.JSONObject
			NextIndex, Total int
		}
		Blocked []string
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.Rows) != len(rows) || len(source.Blocked) > 0 {
		t.Fatal(e, string(out))
	}
	for i, row := range rows {
		filter := rawMain(row["filter"])
		picker, e := NewCLIResumePicker(ctx, f.n, "darwin", filter)
		if e != nil {
			t.Fatal(e)
		}
		reset := false
		count := row["count"].(int)
		logs, more, e := picker.ReadResumePicker(ctx, &reset, count)
		if e != nil {
			t.Fatal(e)
		}
		want := source.Rows[i]
		if picker.cursor != want.NextIndex || len(picker.stats) != want.Total || more != (want.NextIndex < want.Total) || len(logs) != len(want.Logs) {
			t.Fatalf("row%d selection counts Go%d/%d logs%d more%t Source%d/%d logs%d", i, picker.cursor, len(picker.stats), len(logs), more, want.NextIndex, want.Total, len(want.Logs))
		}
		for j, log := range logs {
			for _, key := range []string{"sessionId", "fullPath", "projectPath", "firstPrompt", "customTitle", "tag", "prNumber", "prUrl", "prRepository", "gitBranch", "summary", "agentSetting", "created", "modified", "isSidechain", "value"} {
				a, b := log.Get(key), want.Logs[j].Get(key)
				if len(a) == 0 != (len(b) == 0) || len(a) > 0 && !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(a), protocol.StringifyValidatedJavaScriptJSON(b)) {
					t.Fatalf("row%d log%d field%s Go%s Source%s", i, j, key, a, b)
				}
			}
		}
	}
	// Prefix match and duplicate selection are stat-only; no transcript/model
	// mutation or provider transport is involved.
	for path, before := range originals {
		after, e := os.ReadFile(path)
		if e != nil || !reflect.DeepEqual(before, after) {
			t.Fatal("picker changed protected transcript", path, e)
		}
	}
}
