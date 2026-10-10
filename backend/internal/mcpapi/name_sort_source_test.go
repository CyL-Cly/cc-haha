package mcpapi

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestAPINamesLocaleCompareMatchesActualSourceUTF16AndStableEquality(t *testing.T) {
	groups := []json.RawMessage{
		json.RawMessage(`["claude.ai \ufffd","claude.ai \ud800 (2)","claude.ai Alpha","claude.ai alpha"]`),
		json.RawMessage(`["z","a","Z","A","e","é","e\u0301","É"]`),
		json.RawMessage(`["中文","日本","한국","Ω","omega","α","alpha","😀","👨‍👩‍👧‍👦"]`),
		json.RawMessage(`["a10","a2","a02","a1","a_","a-","a!","a.","a%","a "," a","a\u0000","a"]`),
		json.RawMessage(`["","\u0000","\ufffd","\ud800","\udfff","\udc00","\ufffe","\uffff","𐀀"]`),
		json.RawMessage(`["ß","ss","ẞ","sS","Å","A\u030a","A","a","æ","ae","œ","oe"]`),
		json.RawMessage(`["a/b","a+b","a=b","a?b","a#b","a:b","a;b","a@b","a[b","a(b","a_b","a-b"]`),
	}
	root, _ := filepath.EvalSymlinks(t.TempDir())
	home := filepath.Join(root, "home")
	os.Mkdir(home, 0700)
	fixture, _ := filepath.Abs("../../testdata/mcp-api-claudeai.fixture.ts")
	input, _ := json.Marshal(map[string]any{"sortGroups": groups})
	command := exec.Command(os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	command.Dir = home
	command.Stdin = bytes.NewReader(input)
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + root, "CC_HAHA_CLAUDEAI_API_FIXTURE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	output, e := command.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(output))
	}
	var source struct {
		Locale, ICU string
		Groups      [][]json.RawMessage
	}
	if json.Unmarshal(output, &source) != nil {
		t.Fatal(string(output))
	}
	t.Log("actual Source Intl", source.Locale, "ICU", source.ICU)
	for index, group := range groups {
		var names []json.RawMessage
		json.Unmarshal(group, &names)
		rows := []protocol.JSONObject{}
		for _, name := range names {
			rows = append(rows, protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: name}))
		}
		e := sortServerNames(rows)
		if runtime.GOOS != "darwin" {
			t.Log("native/fallback Unicode result requires this OS Source comparison")
		}

		if e != nil {
			t.Fatal(e)
		}
		actual := [][]uint16{}
		expected := [][]uint16{}
		for _, row := range rows {
			units, e := protocol.DecodeJavaScriptJSONString(row.Get("name"))
			if e != nil {
				t.Fatal(e)
			}
			actual = append(actual, units)
		}
		for _, name := range source.Groups[index] {
			units, e := protocol.DecodeJavaScriptJSONString(name)
			if e != nil {
				t.Fatal(e)
			}
			expected = append(expected, units)
		}
		if !reflect.DeepEqual(actual, expected) {
			t.Fatalf("group%d Source=%s Go=%v", index, source.Groups[index], rows)
		}
	}
}
func TestAPINamesParallelCollatorsHaveIndependentLifecycle(t *testing.T) {
	var workers sync.WaitGroup
	failures := make(chan error, 12)
	for i := 0; i < 12; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for n := 0; n < 30; n++ {
				rows := []protocol.JSONObject{protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: json.RawMessage(`"Zulu"`)}), protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: json.RawMessage(`"alpha"`)})}
				if err := sortServerNames(rows); err != nil {
					failures <- err
					return
				}
				if rows[0].String("name") != "alpha" {
					failures <- os.ErrInvalid
					return
				}
			}
		}()
	}
	workers.Wait()
	close(failures)
	for e := range failures {
		t.Error(e)
	}
}
