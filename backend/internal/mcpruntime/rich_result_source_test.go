package mcpruntime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func runMCPRichSource(t *testing.T, home string, cases []json.RawMessage, obstruct bool, extra ...map[string]any) protocol.JSONObject {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual pinned Source Bun required")
	}
	fixture, e := filepath.Abs("../../testdata/native-mcp-rich-result.fixture.ts")
	if e != nil {
		t.Fatal(e)
	}
	work := filepath.Join(home, "work")
	os.MkdirAll(work, 0700)
	cmd := exec.Command(bun, "run", fixture)
	cmd.Dir = home
	cmd.Env = []string{"PATH=" + filepath.Dir(bun) + ":/usr/bin:/bin", "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CC_HAHA_FIXTURE_TEMP_ROOT=" + os.TempDir(), "CC_HAHA_MCP_RICH_RESULT_FIXTURE=1"}
	input := map[string]any{"work": work, "session": "11111111-1111-4111-8111-111111111119", "now": int64(1770000000000), "randoms": []float64{.5, .25, .75}, "server": "claude.ai 🧡 @blob", "cases": cases, "obstruct": obstruct}
	for _, values := range extra {
		for key, value := range values {
			input[key] = value
		}
	}
	cmd.Stdin = bytes.NewReader(resultRaw(input))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	p, e := cmd.Output()
	if e != nil {
		t.Fatal("actual Source rich transform", e, stderr.String())
	}
	return resultObject(p)
}
func TestActualMCPAudioAndResourceBlobUsePhysicalFilesAndSourceInstructions(t *testing.T) {
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	cases := []json.RawMessage{[]byte(`{"content":[{"type":"audio","data":"AAE/Cg==","mimeType":"audio/wav"},{"type":"resource","resource":{"uri":"owned:///private","mimeType":"text/plain; charset=utf-8","blob":"TUNQX1JJQ0hfUEhZU0lDQUxfRklMRQo="}}],"structuredContent":{"ok":true}}`)}
	want := runMCPRichSource(t, home, cases, false)
	var wantRows []protocol.JSONObject
	json.Unmarshal(want.Get("rows"), &wantRows)
	randoms := []float64{.5, .25, .75}
	index := 0
	got, e := TransformRichResult(context.Background(), resultObject(cases[0]), "contract-tool", "claude.ai 🧡 @blob", RichResultOptions{Directory: want.String("directory"), NowMillis: func() float64 { return 1770000000000 }, Random: func() float64 { v := randoms[index]; index++; return v }})
	if e != nil {
		t.Fatal("ordinary generic MCP rich producer did not reach Source file consumer", e)
	}
	if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(got), protocol.StringifyValidatedJavaScriptJSON(wantRows[0].Get("content"))) {
		t.Fatalf("actual Source content mismatch\nGo%s\nSource%s", got, wantRows[0].Get("content"))
	}
}

func TestActualMCPRichRawUTF16CoercionMIMEEmptyImageAndFilesystemFailure(t *testing.T) {
	cases := []json.RawMessage{}
	for _, s := range []string{
		`{"content":[{"type":"image","data":"","mimeType":"image/png"}]}`,
		`{"content":[{"type":"image","data":7,"mimeType":"image/png"}]}`,
		`{"content":[{"type":"image","data":"AAE/Cg==","mimeType":true}]}`,
		`{"content":[{"type":"audio","data":"AAE/Cg-_===tail\ud800","mimeType":"audio/mpeg; charset=utf-8"}]}`,
		`{"content":[{"type":"audio","data":[]} ]}`,
		`{"content":[{"type":"audio","data":[1,255,256,-1,"5",true,null]}]}`,
		`{"content":[{"type":"audio","data":null}]}`,
		`{"content":[{"type":"audio","data":7}]}`,
		`{"content":[{"type":"audio","data":"AA==","mimeType":false}]}`,
		`{"content":[{"type":"audio","data":"AA==","mimeType":0}]}`,
		`{"content":[{"type":"resource","resource":{"uri":"owned:///\ud800","text":null}}]}`,
		`{"content":[{"type":"resource_link","name":null,"uri":7,"description":[]}]}`,
		`{"content":[{"type":"resource","resource":{"uri":"owned:///weird","mimeType":"image/png; charset=utf-8","blob":"AAE/Cg=="}}]}`,
		`{"content":[{"type":"resource","resource":{"uri":"owned:///svg","mimeType":"image/svg+xml","blob":"PHN2Zz48L3N2Zz4="}}]}`,
		`{"content":[{"type":"audio","data":"AAE/Cg==","mimeType":7}]}`,
		`{"content":[null]}`,
		`{"content":[{"type":"resource","resource":null}]}`,
	} {
		cases = append(cases, json.RawMessage(s))
	}
	cases = append(cases, resultRaw(map[string]any{"content": []any{map[string]any{"type": "audio", "data": base64.StdEncoding.EncodeToString(make([]byte, 1280)), "mimeType": "audio/wav"}}}))
	for _, obstruct := range []bool{false, true} {
		t.Run(map[bool]string{false: "files", true: "write-failure"}[obstruct], func(t *testing.T) {
			home, e := filepath.EvalSymlinks(t.TempDir())
			if e != nil {
				t.Fatal(e)
			}
			want := runMCPRichSource(t, home, cases, obstruct)
			directory := want.String("directory")
			if !obstruct {
				os.RemoveAll(directory)
			}
			randoms := []float64{.5, .25, .75}
			index := 0
			rows := []any{}
			for _, p := range cases {
				settled := make(chan struct{})
				got, e := TransformRichResult(context.Background(), resultObject(p), "contract-tool", "claude.ai 🧡 @blob", RichResultOptions{Directory: directory, NowMillis: func() float64 { return 1770000000000 }, Random: func() float64 {
					n := .5
					if index < len(randoms) {
						n = randoms[index]
					}
					index++
					return n
				}, OnSettled: func() { close(settled) }})
				<-settled
				var failure any
				if e != nil {
					failure = e.Error()
					got = nil
				}
				rows = append(rows, map[string]any{"content": got, "error": failure})
			}
			files := []any{}
			if !obstruct {
				entries, e := os.ReadDir(directory)
				if e != nil {
					t.Fatal(e)
				}
				for _, entry := range entries {
					data, e := os.ReadFile(filepath.Join(directory, entry.Name()))
					if e != nil {
						t.Fatal(e)
					}
					files = append(files, map[string]any{"name": entry.Name(), "data": base64.StdEncoding.EncodeToString(data)})
				}
			}
			got := resultRaw(map[string]any{"rows": rows, "files": files, "directory": directory, "randomIndex": index})
			if !jsonEqual(got, resultRaw(want)) {
				var wantRows []json.RawMessage
				json.Unmarshal(want.Get("rows"), &wantRows)
				for i, row := range rows {
					if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(resultRaw(row)), protocol.StringifyValidatedJavaScriptJSON(wantRows[i])) {
						t.Errorf("case%d Source%s Go%s", i, wantRows[i], resultRaw(row))
					}
				}
				t.Fatalf("Source physical files/RNG/source shape mismatch\nGo%s\nSource%s", got, resultRaw(want))
			}
		})
	}
}

func TestActualMCPSourcePromiseAllRejectsBeforeHeldWritesAndRetainsAllFileSideEffects(t *testing.T) {
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	cases := []json.RawMessage{[]byte(`{"content":[{"type":"image","data":"","mimeType":"image/png"},{"type":"audio","data":"AAE/Cg==","mimeType":"audio/wav"},{"type":"resource","resource":{"uri":"owned:///private","mimeType":"text/plain","blob":"TUNQX1JJQ0hfUEhZU0lDQUxfRklMRQo="}}]}`)}
	want := runMCPRichSource(t, home, cases, false, map[string]any{"parallel": true})
	directory := want.String("directory")
	os.RemoveAll(directory)
	started := make(chan struct{}, 2)
	release, settled := make(chan struct{}), make(chan struct{})
	var startCount, completeCount atomic.Int32
	randoms := []float64{.5, .25}
	index := 0
	got, e := TransformRichResult(context.Background(), resultObject(cases[0]), "contract-tool", "claude.ai 🧡 @blob", RichResultOptions{Directory: directory, NowMillis: func() float64 { return 1770000000000 }, Random: func() float64 { n := randoms[index]; index++; return n }, WriteFile: func(path string, data []byte) error {
		startCount.Add(1)
		started <- struct{}{}
		<-release
		e := os.WriteFile(path, data, 0666)
		completeCount.Add(1)
		return e
	}, OnSettled: func() { close(settled) }})
	if e == nil {
		t.Fatal("Source empty image did not reject shared transformation")
	}
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("Promise.all sibling write did not start")
		}
	}
	select {
	case <-settled:
		t.Fatal("native lifetime released while actual blob writes remain pending")
	default:
	}
	before := map[string]any{"started": startCount.Load(), "completed": completeCount.Load(), "rejected": e != nil}
	failure := e.Error()
	close(release)
	select {
	case <-settled:
	case <-time.After(time.Second):
		t.Fatal("actual all-settled native lifetime not released")
	}
	files := []any{}
	entries, e := os.ReadDir(directory)
	if e != nil {
		t.Fatal(e)
	}
	for _, entry := range entries {
		data, e := os.ReadFile(filepath.Join(directory, entry.Name()))
		if e != nil {
			t.Fatal(e)
		}
		files = append(files, map[string]any{"name": entry.Name(), "data": base64.StdEncoding.EncodeToString(data)})
	}
	value := resultRaw(map[string]any{"rows": []any{map[string]any{"content": got, "error": failure}}, "files": files, "directory": directory, "randomIndex": index, "parallel": map[string]any{"before": before, "after": map[string]any{"started": startCount.Load(), "completed": completeCount.Load()}}})
	if !jsonEqual(value, resultRaw(want)) {
		t.Fatalf("actual Source fail-fast/late files/native lifetime mismatch\nGo%s\nSource%s", value, resultRaw(want))
	}
}
