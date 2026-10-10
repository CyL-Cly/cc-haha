package opentargets

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type nativeFixture struct {
	Commands     map[string]string          `json:"commands"`
	Exists       []string                   `json:"exists"`
	Dirs         map[string][]string        `json:"dirs"`
	Texts        map[string]string          `json:"texts"`
	Plists       map[string]string          `json:"plists"`
	Applications map[string]json.RawMessage `json:"applications"`
	Icons        map[string]string          `json:"icons"`
	LaunchCode   int                        `json:"launchCode"`
}
type scenario struct {
	CWD        string        `json:"cwd"`
	Platform   string        `json:"platform"`
	Now        int64         `json:"now"`
	TTL        int64         `json:"ttl"`
	Native     nativeFixture `json:"native"`
	Operations []operation   `json:"operations"`
}
type operation struct {
	Kind   string         `json:"kind,omitempty"`
	Method string         `json:"method,omitempty"`
	Path   string         `json:"path,omitempty"`
	Body   *string        `json:"body,omitempty"`
	Force  bool           `json:"force,omitempty"`
	Delta  int64          `json:"delta,omitempty"`
	Native *nativeFixture `json:"native,omitempty"`
}
type observation struct {
	Status int     `json:"status"`
	Body   string  `json:"body"`
	Type   string  `json:"type"`
	Cache  *string `json:"cache"`
}
type launchCall struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}
type iconCall struct {
	Path string `json:"path"`
	Size int    `json:"size"`
}
type outcomes struct {
	Observations []observation `json:"observations"`
	Launches     []launchCall  `json:"launches"`
	Icons        []iconCall    `json:"icons"`
	Queries      []string      `json:"queries"`
}

func fixture(t *testing.T, platform string) (string, scenario) {
	t.Helper()
	home := t.TempDir()
	home, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(home, "work")
	for _, p := range []string{cwd, filepath.Join(home, ".claude"), filepath.Join(home, "tmp")} {
		if err = os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	n := nativeFixture{Commands: map[string]string{}, Exists: []string{}, Dirs: map[string][]string{}, Texts: map[string]string{}, Plists: map[string]string{}, Applications: map[string]json.RawMessage{}, Icons: map[string]string{}}
	return home, scenario{CWD: cwd, Platform: platform, Now: 1791200000000, TTL: 30000, Native: n, Operations: []operation{}}
}
func ownedFile(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("content"), mode); err != nil {
		t.Fatal(err)
	}
}
func textBody(text string) *string { return &text }
func openOp(id, path string) operation {
	raw, _ := json.Marshal(map[string]string{"targetId": id, "path": path})
	return operation{Method: "POST", Path: "/api/open-targets/open", Body: textBody(string(raw))}
}
func queryOp(path string) operation {
	return operation{Method: "GET", Path: "/api/open-targets?path=" + url.QueryEscape(path)}
}
func iconOp(id string) operation {
	return operation{Method: "GET", Path: "/api/open-targets/icons/" + encodeURIComponent(id)}
}
func decode(t *testing.T, text string) any {
	t.Helper()
	var result any
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatal(err, text)
	}
	return result
}
func runScenario(t *testing.T, home string, row scenario) (*Service, outcomes) {
	t.Helper()
	native, now := row.Native, row.Now
	out := outcomes{Observations: []observation{}, Launches: []launchCall{}, Icons: []iconCall{}, Queries: []string{}}
	primitives := Primitives{
		CommandExists:  func(_ context.Context, c string) bool { return native.Commands[c] != "" },
		ResolveCommand: func(_ context.Context, c string) (string, error) { return native.Commands[c], nil },
		PathExists: func(p string) bool {
			for _, existing := range native.Exists {
				if p == existing {
					return true
				}
			}
			return false
		},
		Launch: func(_ context.Context, c string, args []string) (LaunchResult, error) {
			out.Launches = append(out.Launches, launchCall{c, args})
			return LaunchResult{Code: native.LaunchCode}, nil
		},
		ReadDirNames: func(p string) []string { return native.Dirs[p] },
		ReadTextFile: func(p string) *string {
			if text, ok := native.Texts[p]; ok {
				return &text
			}
			return nil
		},
		ReadPlistValue: func(_ context.Context, p, key string) *string {
			if text, ok := native.Plists[p+":"+key]; ok {
				return &text
			}
			return nil
		},
		ConvertIconToPNG: func(_ context.Context, p string, size int) ([]byte, error) {
			out.Icons = append(out.Icons, iconCall{p, size})
			data, ok := native.Icons[p]
			if !ok {
				return nil, fmt.Errorf("native icon conversion failed")
			}
			return base64.StdEncoding.DecodeString(data)
		},
		ListApplicationsForFile: func(_ context.Context, p string) (ApplicationList, error) {
			out.Queries = append(out.Queries, p)
			if raw, ok := native.Applications[p]; ok {
				return ParseApplicationList(raw), nil
			}
			return ApplicationList{Applications: []Application{}}, nil
		},
	}
	s, err := New(Options{Home: home, ConfigDir: filepath.Join(home, ".claude"), CWD: row.CWD, Platform: row.Platform, Environment: map[string]string{"HOME": home, "PATH": "/usr/bin:/bin"}, TempDir: filepath.Join(home, "tmp"), Now: func() int64 { return now }, TTLMillis: &row.TTL, Primitives: primitives})
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range row.Operations {
		if op.Kind == "advance" {
			now += op.Delta
			continue
		}
		if op.Kind == "mutate" {
			native = *op.Native
			continue
		}
		if op.Kind == "list" {
			list, err := s.List(context.Background(), op.Force)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := json.Marshal(list)
			out.Observations = append(out.Observations, observation{Status: 200, Body: string(body), Type: "application/json"})
			continue
		}
		body := ""
		if op.Body != nil {
			body = *op.Body
		}
		r := httptest.NewRequest(op.Method, op.Path, strings.NewReader(body))
		w := httptest.NewRecorder()
		s.HandleHTTP(w, r, strings.Split(strings.Trim(r.URL.EscapedPath(), "/"), "/"))
		content := w.Body.String()
		if w.Header().Get("Content-Type") == "image/png" {
			content = base64.StdEncoding.EncodeToString(w.Body.Bytes())
		}
		var cache *string
		if value := w.Header().Get("Cache-Control"); value != "" {
			cache = &value
		}
		out.Observations = append(out.Observations, observation{w.Code, content, w.Header().Get("Content-Type"), cache})
	}
	compareSource(t, home, row, out)
	return s, out
}
func jsonApps(apps []Application, defaultPath *string) json.RawMessage {
	raw, _ := json.Marshal(ApplicationList{defaultPath, apps})
	return raw
}
func app(path, id, name string, isDefault bool, count float64) Application {
	var bundle *string
	if id != "" {
		bundle = &id
	}
	return Application{AppPath: path, BundleID: bundle, DisplayName: name, IsDefault: isDefault, UseCount: &count}
}

func TestDarwinTargetsRankingDedupeLaunchIconsAndTTLAgainstActualSource(t *testing.T) {
	home, row := fixture(t, "darwin")
	file := filepath.Join(row.CWD, "report.pdf")
	ownedFile(t, file, 0600)
	row.Native.Exists = []string{"/Applications/Visual Studio Code.app", "/Applications/Cursor.app", "/System/Library/CoreServices/Finder.app/Contents/Resources/Finder.icns"}
	canonical := "/Applications/Preview.app"
	copy := app(filepath.Join(home, "Library", "Caches", "Preview.app"), "com.apple.Preview", "Preview Cache", false, 10000)
	copy.CanonicalPath = &canonical
	apps := []Application{copy, app(canonical, "com.apple.Preview", "Preview", false, 1), app("/Applications/Reader.app", "reader", "Reader", true, 30), app("/Applications/Visual Studio Code.app", "com.microsoft.VSCode", "VS Code", false, 5), app("/Applications/Host.app/Contents/Helpers/Hidden.app", "helper", "Hidden", false, 50), app("/Applications/Our.app", "com.claude-code-haha.desktop", "Our App", false, 40), app("/Applications/ZZ.app", "zz", "ZZ", false, 200), app("/Volumes/Disk/Applications/A.app", "a", "A", false, 1), app("/Downloads/B.app", "b", "B", false, 500), app("/Downloads/C.app", "c", "C", false, 400), app("/Downloads/D.app", "d", "D", false, 300)}
	row.Native.Applications[file] = jsonApps(apps, nil)
	previewID := applicationID(apps[1])
	readerID := applicationID(apps[2])
	iconPath := "/Applications/Reader.app/Contents/Resources/AppIcon.icns"
	row.Native.Exists = append(row.Native.Exists, iconPath)
	row.Native.Plists["/Applications/Reader.app/Contents/Info.plist:CFBundleIconFile"] = "AppIcon"
	row.Native.Icons[iconPath] = base64.StdEncoding.EncodeToString([]byte("PNG native primitive bytes"))
	row.Operations = []operation{{Method: "GET", Path: "/api/open-targets"}, queryOp(file), iconOp(readerID), iconOp(readerID), openOp(readerID, file), openOp(previewID, file), openOp("finder", file), openOp("vscode", file), {Kind: "advance", Delta: 30001}, queryOp(file), iconOp("application:unregistered")}
	s, out := runScenario(t, home, row)
	if len(out.Queries) != 2 || len(out.Icons) != 1 || len(out.Launches) != 4 {
		t.Fatalf("cache/launch primitives %+v", out)
	}
	var result List
	if err := json.Unmarshal([]byte(out.Observations[1].Body), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Targets) != 9 || result.PrimaryTargetID == nil || *result.PrimaryTargetID != readerID || result.Targets[1].ID != previewID {
		t.Fatalf("full ranking %+v", result)
	}
	if _, err := s.GetIcon(context.Background(), "application:"+base64.RawURLEncoding.EncodeToString([]byte("/etc/passwd")), 64); err == nil {
		t.Fatal("unissued application id authorized a read")
	}
}

func TestWindowsLaunchersAssociationsIconsAndSourceErrors(t *testing.T) {
	home, row := fixture(t, "win32")
	file := filepath.Join(row.CWD, "report.html")
	ownedFile(t, file, 0600)
	row.Native.Commands = map[string]string{"code.cmd": `C:\Apps\VS Code\bin\code.cmd`, "cursor.exe": `C:\Apps\Cursor\Cursor.exe`, "explorer.exe": `C:\Windows\explorer.exe`}
	row.Native.Exists = []string{`C:\Apps\VS Code\Code.exe`, `C:\Apps\Cursor\Cursor.exe`}
	row.Native.Icons[`C:\Apps\VS Code\Code.exe`] = base64.StdEncoding.EncodeToString([]byte("code icon"))
	row.Native.Icons[`C:\Windows\explorer.exe`] = base64.StdEncoding.EncodeToString([]byte("explorer icon"))
	row.Operations = []operation{{Method: "GET", Path: "/api/open-targets"}, queryOp(file), openOp("vscode", file), openOp("cursor", file), openOp("explorer", file), openOp("explorer", row.CWD), openOp("system-default", file), iconOp("vscode"), iconOp("explorer"), openOp("goland", file), openOp("not-a-target", file)}
	_, out := runScenario(t, home, row)
	if len(out.Queries) != 0 || len(out.Launches) != 5 || out.Launches[0].Command != `C:\Apps\VS Code\Code.exe` || out.Launches[2].Args[0] != "/select,"+file || out.Launches[3].Command != "cmd.exe" {
		t.Fatalf("Windows launch contract %+v", out)
	}
	for _, i := range []int{9, 10} {
		if out.Observations[i].Status != 400 {
			t.Fatal(out.Observations[i])
		}
	}
}

func TestLinuxDesktopEntriesThemeIconsAndSystemOpening(t *testing.T) {
	home, row := fixture(t, "linux")
	file := filepath.Join(row.CWD, "test.txt")
	ownedFile(t, file, 0600)
	row.Native.Commands = map[string]string{"code": "/bin/code", "zeditor": "/bin/zeditor", "xdg-open": "/bin/xdg-open"}
	dir := filepath.Join(home, ".local", "share", "applications")
	row.Native.Dirs[dir] = []string{"not.desktop", "alternate.desktop", "code.desktop"}
	row.Native.Texts[filepath.Join(dir, "not.desktop")] = "[Desktop Entry]\nName=Missing icon and exec\n"
	row.Native.Texts[filepath.Join(dir, "alternate.desktop")] = "[Not Desktop Entry]\nIcon=wrong\n[Desktop Entry]\nName=Something Else\nName[zh_CN]=其他\nExec=env FLAG=value '/usr/bin/code' %F\nIcon=visual-code\n"
	icon := filepath.Join(home, ".local", "share", "icons", "theme", "scalable", "apps", "visual-code.svg")
	row.Native.Dirs[filepath.Join(home, ".local", "share", "icons")] = []string{"theme"}
	row.Native.Exists = []string{icon, "/usr/share/pixmaps/folder.png"}
	row.Native.Icons[icon] = base64.StdEncoding.EncodeToString([]byte("SVG rasterized icon"))
	row.Native.Icons["/usr/share/pixmaps/folder.png"] = base64.StdEncoding.EncodeToString([]byte("folder"))
	row.Operations = []operation{{Method: "GET", Path: "/api/open-targets"}, queryOp(file), openOp("file-manager", file), openOp("system-default", file), openOp("vscode", file), iconOp("vscode"), iconOp("file-manager"), iconOp("cursor")}
	_, out := runScenario(t, home, row)
	if len(out.Queries) != 0 || len(out.Icons) != 2 || out.Icons[0].Path != icon || out.Launches[0].Args[0] != row.CWD {
		t.Fatalf("Linux discovery/launch/icon %+v", out)
	}
}

func TestOpenTargetsHTTPValidationAndExecutableRulesAgainstSource(t *testing.T) {
	for _, platform := range []string{"darwin", "linux", "win32", "freebsd"} {
		t.Run(platform, func(t *testing.T) {
			home, row := fixture(t, platform)
			file := filepath.Join(row.CWD, "ordinary.txt")
			ownedFile(t, file, 0600)
			executable := filepath.Join(row.CWD, "executable")
			ownedFile(t, executable, 0700)
			script := filepath.Join(row.CWD, "file.SH")
			ownedFile(t, script, 0600)
			hiddenScript := filepath.Join(row.CWD, ".sh")
			ownedFile(t, hiddenScript, 0600)
			row.Native.Commands = map[string]string{"xdg-open": "/bin/xdg-open"}
			row.Operations = []operation{{Method: "POST", Path: "/api/open-targets"}, {Method: "GET", Path: "/api/open-targets/open"}, {Method: "POST", Path: "/api/open-targets/icons/finder"}, {Method: "GET", Path: "/api/open-targets/unknown"}, {Method: "GET", Path: "/api/open-targets/icons"}, iconOp("unknown"), iconOp("system-default"), queryOp("missing.txt"), openOp("unknown", "missing.txt"), openOp("system-default", executable), openOp("system-default", script), openOp("system-default", hiddenScript)}
			row.Operations = append(row.Operations, operation{Method: "GET", Path: "/api/open-targets/icons/%ED%A0%80"})
			for _, raw := range []string{"{", `null`, `[]`, `3`, `{"targetId":3}`, `{"targetId":"\u00a0","path":"x"}`, `{"targetId":"finder"}`, `{"targetId":"finder","path":" \u2028 "}`} {
				row.Operations = append(row.Operations, operation{Method: "POST", Path: "/api/open-targets/open", Body: textBody(raw)})
			}
			_, out := runScenario(t, home, row)
			if out.Observations[0].Status != 405 || out.Observations[3].Status != 404 || out.Observations[4].Status != 400 || out.Observations[8].Status != 400 {
				t.Fatalf("HTTP validation %+v", out.Observations)
			}
			if platform != "freebsd" && out.Observations[11].Status != 200 {
				t.Fatal("dotfile extension differs from Node extname", out.Observations[11])
			}
		})
	}
}
