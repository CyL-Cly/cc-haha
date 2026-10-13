package sdkpermissions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestWindowsPermissionStartupAgainstActualSourceInitializerAndPhysicalDirectoryPrimitives(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun required")
	}
	home := t.TempDir()
	physical := map[string]string{}
	for i, path := range []string{`C:\Project`, `C:\Project\sub`, `D:\Outside`, `D:\Outside\nested`, `C:\Users\worker\outside`, `C:\plain.txt`, `\\server\share\outside`, `C:\Alias`, `C:\Alias\sub`} {
		disk := filepath.Join(home, fmt.Sprint(i))
		physical[path] = disk
		if path == `C:\plain.txt` {
			if err := os.WriteFile(disk, []byte("plain"), 0600); err != nil {
				t.Fatal(err)
			}
		} else if err := os.MkdirAll(disk, 0700); err != nil {
			t.Fatal(err)
		}
	}
	type row struct {
		Settings  map[string]any    `json:"settings,omitempty"`
		AddDirs   []string          `json:"addDirs,omitempty"`
		PWD       string            `json:"pwd,omitempty"`
		Canonical map[string]string `json:"canonical,omitempty"`
		Failures  map[string]string `json:"failures,omitempty"`
	}
	rows := []row{
		{AddDirs: []string{`/c/Project/sub`, `c:\PROJECT\sub`, `D:\Outside\`, `D:\Outside\nested`, `D:\Outside`, "", `../plain.txt`, `~/outside`, `C:\missing`}},
		{Settings: map[string]any{"permissions": map[string]any{"additionalDirectories": []string{`D:\Outside`, `D:\Outside\nested`}}}, AddDirs: []string{`D:\Outside\`}},
		{PWD: `C:\Alias`, Canonical: map[string]string{`C:\Alias`: `C:\Project`}, AddDirs: []string{`C:\Alias\sub`, `D:\Outside`}},
		{PWD: `\\server\share\outside`, AddDirs: []string{`\\server\share\outside`}},
	}
	for _, code := range []string{"ENOENT", "ENOTDIR", "EACCES", "EPERM", "EIO"} {
		rows = append(rows, row{AddDirs: []string{`D:\Outside`}, Failures: map[string]string{`D:\Outside`: code}})
	}
	payload, _ := json.Marshal(map[string]any{"home": home, "nativeHome": `C:\Users\worker`, "cwd": `C:\Project`, "paths": physical, "rows": rows})
	fixture, _ := filepath.Abs("../../testdata/native-windows-permission-startup.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_WINDOWS_STARTUP_FIXTURE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Source initializer %v %.3000s", err, out)
	}
	var source struct {
		Rows []struct {
			Directories json.RawMessage
			Warnings    []string
			Error       string
			Accessed    []string
		}
	}
	if err := json.Unmarshal(out, &source); err != nil || len(source.Rows) != len(rows) {
		t.Fatalf("Source shape %v %.2000s", err, out)
	}
	for i, r := range rows {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			owner := cooperative.New()
			defer owner.Close()
			accessed := []string{}
			mapPath := func(path string) (string, error) {
				accessed = append(accessed, path)
				if code := r.Failures[path]; code != "" {
					var cause error
					switch code {
					case "ENOENT":
						cause = fs.ErrNotExist
					case "ENOTDIR":
						cause = errors.New("not directory")
					case "EACCES":
						cause = syscall.EACCES
					case "EPERM":
						cause = syscall.EPERM
					default:
						cause = errors.New(code)
					}
					if code == "ENOTDIR" {
						cause = syscall.ENOTDIR
					}
					return "", cause
				}
				for key, disk := range physical {
					if strings.EqualFold(key, path) {
						return disk, nil
					}
				}
				return "", fs.ErrNotExist
			}
			stat := func(path string) (fs.FileInfo, error) {
				disk, err := mapPath(path)
				if err != nil {
					return nil, err
				}
				return os.Stat(disk)
			}
			lstat := func(path string) (fs.FileInfo, error) {
				disk, err := mapPath(path)
				if err != nil {
					return nil, err
				}
				return os.Lstat(disk)
			}
			global := protocol.NewJSONObject()
			startup, err := NewNativeStartup(owner, NativeStartupOptions{Home: `C:\Users\worker`, CWD: `C:\Project`, OriginalCWD: `C:\Project`, Platform: "windows", Environment: func(key string) string {
				if key == "PWD" {
					return r.PWD
				}
				return ""
			}, Global: func() (*protocol.JSONObject, error) { return &global, nil }, Stat: stat, Lstat: lstat, Realpath: func(path string) (string, error) {
				disk, err := mapPath(path)
				if err != nil {
					return "", err
				}
				if _, err := filepath.EvalSymlinks(disk); err != nil {
					return "", err
				}
				if value := r.Canonical[path]; value != "" {
					return value, nil
				}
				return path, nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			var settings protocol.JSONObject
			settings.UnmarshalJSON(persistRaw(r.Settings))
			var result NativeStartupResult
			err = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				var err error
				result, err = startup.Prepare(scope, &settings, NativeStartupRequest{AdditionalDirectories: r.AddDirs})
				return err
			})
			if string(persistRaw(accessed)) != string(persistRaw(source.Rows[i].Accessed)) {
				t.Fatalf("actual OS primitive admissions Source%v Go%v", source.Rows[i].Accessed, accessed)
			}
			if source.Rows[i].Error != "" {
				if err == nil || err.Error() != source.Rows[i].Error {
					t.Fatalf("Source error %s Go%v", source.Rows[i].Error, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var values protocol.JSONObject
			values.UnmarshalJSON(result.WorkingDirectories)
			if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(values.Get("map")), protocol.StringifyValidatedJavaScriptJSON(source.Rows[i].Directories)) || string(persistRaw(result.Warnings)) != string(persistRaw(source.Rows[i].Warnings)) {
				t.Fatalf("Source dirs%s warnings%v Go%s warnings%v", source.Rows[i].Directories, source.Rows[i].Warnings, values.Get("map"), result.Warnings)
			}
		})
	}
}
