package nativepaths

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestWindowsPermissionsAgainstActualSourceDriveRootsCaseIgnoreAndOrder(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Bun required")
	}
	type row struct {
		Path  string              `json:"path"`
		CWD   string              `json:"cwd"`
		Home  string              `json:"home"`
		Tool  string              `json:"tool"`
		Rules map[string][]string `json:"rules"`
		Roots map[string]string   `json:"roots"`
	}
	rows := []row{}
	roots := map[string]string{"userSettings": `C:\User\.claude`, "projectSettings": `C:\Work`, "localSettings": `C:\Work`, "policySettings": `C:\Work`, "flagSettings": `D:\Flag`}
	paths := []string{`C:\Work\secret.txt`, `c:\work\secret.txt`, `C:\Work\nested\a.txt`, `C:\Work\public\a.txt`, `C:\Work\folder(a)\a.txt`, `C:\Work\a[1].txt`, `D:\Work\secret.txt`, `C:\User\.claude\private\a.txt`, `D:\Flag\private\a.txt`, `\\server\share\project\a.txt`, `\\server\share\other\a.txt`}
	profiles := []map[string][]string{
		{"projectSettings": {"Edit(secret.txt)"}},
		{"projectSettings": {"Edit(*.txt)", "Edit(!public/**)"}},
		{"projectSettings": {"Edit(/nested/**)"}},
		{"userSettings": {"Edit(/private/**)"}},
		{"flagSettings": {"Edit(/private/**)"}},
		{"session": {"Edit(//c/Work/**)"}},
		{"session": {"Edit(~/private/**)"}},
		{"projectSettings": {"Edit(folder\\(a\\)/**)"}},
		{"projectSettings": {"Edit(a[[]1].txt)"}},
		{"userSettings": {"Edit(/private/**)"}, "projectSettings": {"Edit(/private/**)"}},
		{"projectSettings": {"Read(**/*.txt)", "Edit(other/**)"}},
	}
	for _, p := range paths {
		for _, rules := range profiles {
			for _, tool := range []string{"Edit", "Read"} {
				rows = append(rows, row{Path: p, CWD: `C:\Work`, Home: `C:\User`, Tool: tool, Rules: rules, Roots: roots})
			}
		}
	}
	for _, path := range []string{`\\server\share\project\a.txt`, `\\server\share\other\a.txt`} {
		rows = append(rows, row{Path: path, CWD: `\\server\share\project`, Home: `C:\User`, Tool: "Edit", Rules: map[string][]string{"session": {"Edit(**/*.txt)"}}, Roots: roots})
	}
	payload, _ := json.Marshal(rows)
	fixture, _ := filepath.Abs("../../testdata/native-windows-permissions.fixture.ts")
	home := t.TempDir()
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_WINDOWS_PERMISSION_FIXTURE=1", "PATH=/usr/bin:/bin"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Source: %v %.2000s", err, out)
	}
	var expected []*struct {
		Pattern string `json:"pattern"`
		Source  string `json:"source"`
	}
	if err := json.Unmarshal(out, &expected); err != nil || len(expected) != len(rows) {
		t.Fatalf("Source shape %v %.2000s", err, out)
	}
	for i, r := range rows {
		data, _ := json.Marshal(r.Rules)
		c := protocol.NewJSONObject(protocol.JSONField{Name: "alwaysDenyRules", Value: data})
		patterns, err := WindowsPatterns(c, "alwaysDenyRules", r.Tool)
		if err != nil {
			t.Fatal(err)
		}
		path, err := ExpandWindows(r.Path, r.CWD, r.Home)
		if err != nil {
			t.Fatal(err)
		}
		matched, err := MatchingWindowsPattern(path, r.CWD, r.Home, r.Roots, patterns)
		if err != nil {
			t.Fatalf("row %d %+v %v", i, r, err)
		}
		e := expected[i]
		if (matched == nil) != (e == nil) || matched != nil && (matched.Pattern != e.Pattern || matched.Source != e.Source) {
			t.Errorf("row %d %+v Go=%+v Source=%+v", i, r, matched, e)
		}
	}
}
