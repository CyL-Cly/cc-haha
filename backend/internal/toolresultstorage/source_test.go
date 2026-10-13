package toolresultstorage

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type sourceRow struct {
	Block     protocol.JSONObject `json:"block"`
	Name      string              `json:"name"`
	Threshold float64             `json:"threshold"`
	Existing  *string             `json:"existing,omitempty"`
	Failure   bool                `json:"failure,omitempty"`
}
type fileSnapshot struct{ Name, Bytes string }
type sourceObservation struct {
	Result protocol.JSONObject
	Files  []fileSnapshot
}

func TestActualSourcePhysicalLargeResultsPreviewFailuresAndDuplicateWrites(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	allocation, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(allocation, "source")
	if err = os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	block := func(content json.RawMessage) protocol.JSONObject {
		return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("tool_result")}, protocol.JSONField{Name: "tool_use_id", Value: quote("actual-use")}, protocol.JSONField{Name: "content", Value: content}, protocol.JSONField{Name: "is_error", Value: json.RawMessage(`false`)}, protocol.JSONField{Name: "future", Value: json.RawMessage(`{"preserved":true}`)})
	}
	text := func(v string) json.RawMessage { return protocol.QuoteJavaScriptUTF16(utf16.Encode([]rune(v))) }
	row := func(v json.RawMessage, threshold float64) sourceRow {
		return sourceRow{Block: block(v), Name: "mcp__fixture__actual", Threshold: threshold}
	}
	large := strings.Repeat("x", 60000) + "ACTUAL_FULL_TAIL"
	newline := strings.Repeat("n", 1101) + "\n" + strings.Repeat("y", 3000)
	units := append(utf16.Encode([]rune(strings.Repeat("s", 1999))), 0xd83d, 0xde00)
	units = append(units, utf16.Encode([]rune(strings.Repeat("z", 5000)))...)
	array := func(v json.RawMessage) json.RawMessage {
		raw, _ := json.Marshal([]protocol.JSONObject{protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("text")}, protocol.JSONField{Name: "text", Value: v}, protocol.JSONField{Name: "future", Value: json.RawMessage(`{"2":"ordered","a":true}`)})})
		return raw
	}
	rows := []sourceRow{row(nil, 50000), row(json.RawMessage(`null`), 50000), row(text("\ufeff \n\t"), 50000), row(json.RawMessage(`[]`), 50000), row(text("exact"), 5), row(text(large), 100000), row(text(newline), 50), row(protocol.QuoteJavaScriptUTF16(units), 50), row(array(text(large)), 50000), row(json.RawMessage(`[{"type":"text","text":""},{"type":"tool_reference","tool_name":"Read"}]`), 1), row(json.RawMessage(`[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AA=="}},{"type":"text","text":"oversized"}]`), 1), row(array(protocol.QuoteJavaScriptUTF16(append([]uint16{0xd800}, utf16.Encode([]rune(" literal \" \n漢😀"))...))), 1)}
	existing := "IMMUTABLE_PREVIOUS"
	duplicate := row(text(large), 1)
	duplicate.Existing = &existing
	rows = append(rows, duplicate)
	failure := row(text(large), 1)
	failure.Failure = true
	rows = append(rows, failure)
	thresholds := []map[string]any{{"name": "MCP", "declared": 100000}, {"name": "Bash", "declared": 30000}, {"name": "Grep", "declared": 20000}, {"name": "Read", "declared": 0, "infinity": true}}
	for _, override := range []any{nil, "invalid", true, []any{}, map[string]any{"MCP": 0}, map[string]any{"MCP": -1}, map[string]any{"MCP": "70000"}, map[string]any{"MCP": 70000}, map[string]any{"MCP": 0.5}, map[string]any{"other": 1}} {
		thresholds = append(thresholds, map[string]any{"name": "MCP", "declared": 100000, "overrides": override})
	}
	thresholds = append(thresholds, map[string]any{"name": "Read", "declared": 0, "infinity": true, "overrides": map[string]any{"Read": 1}})
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "rows": rows, "thresholds": thresholds})
	fixture, _ := filepath.Abs("../../testdata/native-tool-result-storage.fixture.ts")
	command := exec.Command(bun, "--no-env-file", fixture)
	command.Dir = home
	command.Stdin = bytes.NewReader(payload)
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "CC_HAHA_TOOL_RESULT_SOURCE_FIXTURE=1", "NODE_ENV=production", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Source process: %v; %.2000s", err, out)
	}
	var source struct {
		Directory    string
		Observations []sourceObservation
		Thresholds   []*float64
		Blocked      []string
	}
	if err = json.Unmarshal(out, &source); err != nil || len(source.Blocked) > 0 || len(source.Observations) != len(rows) {
		t.Fatalf("Source output: %v; %.2000s", err, out)
	}
	for i, r := range rows {
		dir := filepath.Join(allocation, "go", string(rune('a'+i)), "tool-results")
		if err = os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
			t.Fatal(err)
		}
		if r.Failure {
			if err = os.WriteFile(dir, []byte("blocking-file"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if r.Existing != nil {
			os.MkdirAll(dir, 0700)
			os.WriteFile(filepath.Join(dir, "actual-use.txt"), []byte(*r.Existing), 0600)
		}
		result, err := Process(r.Block, r.Name, Threshold(r.Name, r.Threshold, protocol.NewJSONObject()), Files{Directory: dir, MkdirAll: func(p string) error { return os.MkdirAll(p, 0777) }, WriteExclusive: func(p string, data []byte) error {
			f, e := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0666)
			if errors.Is(e, os.ErrExist) {
				return nil
			}
			if e != nil {
				return e
			}
			_, e = f.Write(data)
			ce := f.Close()
			if e != nil {
				return e
			}
			return ce
		}})
		if err != nil {
			t.Fatal(i, err)
		}
		got, _ := result.MarshalJSON()
		expected, _ := source.Observations[i].Result.MarshalJSON()
		got = bytes.ReplaceAll(got, []byte(dir), []byte(source.Directory))
		var a, b any
		json.Unmarshal(got, &a)
		json.Unmarshal(expected, &b)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("profile%d mapped result differs\nGo%s\nSource%s", i, got, expected)
		}
		files := []fileSnapshot{}
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			data, er := os.ReadFile(filepath.Join(dir, e.Name()))
			if er != nil {
				t.Fatal(er)
			}
			files = append(files, fileSnapshot{e.Name(), base64.StdEncoding.EncodeToString(data)})
		}
		if !reflect.DeepEqual(files, source.Observations[i].Files) {
			t.Fatalf("profile%d physical complete file differs (Go%d Source%d)", i, len(files), len(source.Observations[i].Files))
		}
	}
	for i, r := range thresholds {
		declared, _ := r["declared"].(int)
		v := float64(declared)
		if r["infinity"] == true {
			v = math.Inf(1)
		}
		raw, _ := json.Marshal(r["overrides"])
		var override protocol.JSONObject
		_ = json.Unmarshal(raw, &override)
		got := Threshold(r["name"].(string), v, override)
		if source.Thresholds[i] == nil {
			if !math.IsInf(got, 1) {
				t.Fatal(got)
			}
		} else if got != *source.Thresholds[i] {
			t.Fatal(i, got, *source.Thresholds[i])
		}
	}
}
