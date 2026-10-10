package sandboxruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestActualSourceLinuxReadGlobNativeDirectoryEnumerationOrdering(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "repo")
	for _, dir := range []string{"a/deep", "b/deep", ".hidden/inner", "c"} {
		os.MkdirAll(filepath.Join(cwd, dir), 0700)
	}
	for _, file := range []string{"a/x.txt", "a/deep/two.txt", "b/one.txt", "b/deep/z.txt", "c/.file", ".hidden/inner/file"} {
		os.WriteFile(filepath.Join(cwd, file), nil, 0600)
	}
	os.Symlink(filepath.Join(cwd, "a"), filepath.Join(cwd, "link"))
	patterns := []string{"**/*.txt", "a/**", "a/*", "./*/deep/*", "*/?.txt", "missing/*", "/*", "~/*", "a/[xt]*", ".hidden/**", "a/**/two.txt"}
	input, _ := json.Marshal(map[string]any{"cwd": cwd, "patterns": patterns})
	fixture, _ := filepath.Abs("testdata/linux-glob.fixture.ts")
	cmd := exec.Command(os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = bytes.NewReader(input)
	output, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(output))
	}
	var source [][]string
	if e = json.Unmarshal(output, &source); e != nil {
		t.Fatal(e, string(output))
	}
	for i, pattern := range patterns {
		got, e := ExpandLinuxGlob(pattern, PathContext{Home: home, CWD: cwd, Realpath: filepath.EvalSymlinks})
		if e != nil || !reflect.DeepEqual(got, source[i]) {
			t.Fatalf("Source%d %q glob%v; native%v %v", i, pattern, source[i], got, e)
		}
	}
}

func TestActualSourceLinuxGlobUnreadableAndSymlinkCycleEnumeration(t *testing.T) {
	for _, kind := range []string{"unreadable", "cycle", "dangling"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			cwd := filepath.Join(home, "repo")
			os.MkdirAll(filepath.Join(cwd, "first"), 0700)
			os.WriteFile(filepath.Join(cwd, "first", "file"), nil, 0600)
			if kind == "unreadable" {
				path := filepath.Join(cwd, "denied")
				os.Mkdir(path, 0000)
				defer os.Chmod(path, 0700)
			} else if kind == "cycle" {
				os.Symlink(cwd, filepath.Join(cwd, "cycle"))
			} else {
				os.Symlink(filepath.Join(cwd, "absent"), filepath.Join(cwd, "dangling"))
			}
			patterns := []string{"**/*", "first/*", "missing/*"}
			input, _ := json.Marshal(map[string]any{"cwd": cwd, "patterns": patterns})
			fixture, _ := filepath.Abs("testdata/linux-glob.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
			cmd.Stdin = bytes.NewReader(input)
			raw, e := cmd.CombinedOutput()
			if e != nil {
				t.Fatal("Source observation", e, string(raw))
			}
			var source [][]string
			if e = json.Unmarshal(raw, &source); e != nil {
				t.Fatal(e, string(raw))
			}
			for i, p := range patterns {
				got, e := ExpandLinuxGlob(p, PathContext{Home: home, CWD: cwd, Realpath: filepath.EvalSymlinks})
				if e != nil || !reflect.DeepEqual(got, source[i]) {
					t.Fatalf("Source %s %q count%d; Go count%d %v", kind, p, len(source[i]), len(got), e)
				}
			}
		})
	}
}
