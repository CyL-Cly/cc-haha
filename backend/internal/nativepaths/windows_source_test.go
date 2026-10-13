package nativepaths

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

func TestWindowsPathsAgainstActualSourceAndNativeWin32(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Bun required")
	}
	type row struct {
		Value, CWD, Home, Target string
		Join                     []string
	}
	rows := []row{}
	values := []string{"", ".", "..", "../", "a/b/../c/", `C:\Users\Example\`, `C:/Users/./Example/../Name/`, `C:\`, `c:foo`, `C:`, `/c/Users/Example`, `/C/Users/../Other/`, `/cygdrive/c/Users/Example`, `/c`, `/`, `/other/path`, `\\server\share`, `\\server\share\folder\..\file`, `//server/share/folder/`, `\\?\C:\file`, "~", "~/folder/", "  cafe\u0301/文件.txt  ", `C:\a\\b\..\file`, `\\server`, `\file`}
	values = append(values, `\\\server\share\file`, `////server/share/file`, `//server//share///file`, `C:..\file`, `C:..`, `\root\file`, `C:/a//../b//`, `C:/../`)
	for _, value := range values {
		rows = append(rows, row{Value: value, CWD: `C:\Users\Example\project`, Home: `C:\Users\Example`})
	}
	for _, pair := range [][2]string{{`C:\Work`, `c:\work\Child`}, {`C:\Work`, `C:\Work2\Child`}, {`C:\Work`, `D:\Work\Child`}, {`\\server\share\work`, `\\SERVER\SHARE\work\child`}, {`\\server\share`, `\\server\other\file`}, {`C:\ΟΣ`, `c:\ος\file`}, {`C:\İ`, `c:\i\u0307\file`}} {
		rows = append(rows, row{Value: pair[0], Target: pair[1], CWD: `C:\Work`, Home: `C:\Users\Example`})
	}
	for _, join := range [][]string{{`C:\Git\cmd\git.exe`, "..", "..", "bin", "bash.exe"}, {`\\server\share`, "bin", "..", "file"}, {"", "", ""}, {`C:\Work`, `\root`, "file"}} {
		rows = append(rows, row{Value: `C:\Work`, Join: join, CWD: `C:\Work`, Home: `C:\Users\Example`})
	}
	for _, join := range [][]string{{`\`, "server", "share"}, {`//`, "server", "share"}, {`///`, "server", "share"}, {`//server`, "share", "file"}, {`\\server`, "share", "file"}, {`C:\`, `/`, "file"}, {".", ".", "..", "file"}} {
		rows = append(rows, row{Value: `C:\Work`, Join: join, CWD: `C:\Work`, Home: `C:\Users\Example`})
	}
	// Lower-case property names are the Source fixture's native row shape.
	payload := []map[string]any{}
	for _, r := range rows {
		payload = append(payload, map[string]any{"value": r.Value, "cwd": r.CWD, "home": r.Home, "target": r.Target, "join": r.Join})
	}
	data, _ := json.Marshal(payload)
	fixture, _ := filepath.Abs("../../testdata/native-windows-paths.fixture.ts")
	home := t.TempDir()
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir, cmd.Stdin = home, bytes.NewReader(data)
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_WINDOWS_PATH_FIXTURE=1", "PATH=/usr/bin:/bin"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Source: %v %.2000s", err, out)
	}
	var expected []struct {
		POSIX                                                   string `json:"posix"`
		Windows, Clean, Dir, Join, Expanded, Relative, Resolved string
		Within                                                  bool
	}
	if err := json.Unmarshal(out, &expected); err != nil || len(expected) != len(rows) {
		t.Fatalf("Source shape: %v %.2000s", err, out)
	}
	lower := cases.Lower(language.Und)
	for i, r := range rows {
		expanded, err := ExpandWindows(r.Value, r.CWD, r.Home)
		if err != nil {
			t.Fatal(err)
		}
		e := expected[i]
		checks := [][3]string{{"posix", WindowsToPOSIX(r.Value), e.POSIX}, {"windows", POSIXToWindows(r.Value), e.Windows}, {"clean", CleanWindows(r.Value), e.Clean}, {"dir", DirWindows(r.Value), e.Dir}, {"expand", expanded, e.Expanded}, {"resolve", ResolveWindows(r.Value, r.CWD), e.Resolved}}
		if r.Join != nil {
			checks = append(checks, [3]string{"join", JoinWindows(r.Join...), e.Join})
		}
		if r.Target != "" {
			checks = append(checks, [3]string{"relative", RelativeWindows(r.Value, r.Target), e.Relative})
			left, _ := ExpandWindows(r.Value, r.CWD, r.Home)
			right, _ := ExpandWindows(r.Target, r.CWD, r.Home)
			rel := RelativeWindows(lower.String(left), lower.String(right))
			within := rel == "" || rel != ".." && !bytes.Contains([]byte(rel), []byte("../"))
			if within != e.Within {
				t.Errorf("%d within %+v Go=%v Source=%v rel=%q", i, r, within, e.Within, rel)
			}
		}
		for _, c := range checks {
			if c[1] != c[2] {
				t.Errorf("%d %s value=%q Go=%q Source=%q", i, c[0], r.Value, c[1], c[2])
			}
		}
	}
}
