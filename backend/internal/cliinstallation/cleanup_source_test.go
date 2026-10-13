package cliinstallation

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"
)

type cleanupFile struct {
	Name      string `json:"name"`
	AgeMS     int64  `json:"ageMs"`
	Mode      uint32 `json:"mode"`
	Empty     bool   `json:"empty"`
	Directory bool   `json:"directory"`
}
type cleanupLease struct {
	Name      string `json:"name"`
	AgeMS     int64  `json:"ageMs"`
	Directory bool   `json:"directory"`
	Active    bool   `json:"active"`
	Malformed bool   `json:"malformed"`
}
type cleanupProfile struct {
	PID     bool           `json:"pid"`
	Files   []cleanupFile  `json:"files"`
	Staging []cleanupFile  `json:"staging"`
	Locks   []cleanupLease `json:"locks"`
	Symlink string         `json:"symlink"`
}
type cleanupObservation struct {
	Versions, Staging, Locks []string
	Symlink                  *string
}

func TestActualSourceCleanupOldVersionsPhysicalDirectories(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	allocation, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(allocation, "source-home")
	if err = os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	hour := int64(time.Hour / time.Millisecond)
	files := []cleanupFile{{"1.0.1", 7 * hour, 0755, false, false}, {"1.0.2", 6 * hour, 0755, false, false}, {"1.0.3", 5 * hour, 0755, false, false}, {"1.0.4", 4 * hour, 0755, false, false}, {"1.0.5", 3 * hour, 0755, false, false}, {"plain-data", hour, 0600, false, false}, {"empty-version", 8 * hour, 0600, true, false}, {"version-directory", 0, 0755, false, true}, {"1.2.3.tmp.42.1234", 2 * hour, 0755, false, false}, {"1.2.3.tmp.42.1235", 0, 0755, false, false}, {"unrelated.tmp-name", 9 * hour, 0600, false, false}}
	profiles := []cleanupProfile{
		{false, files, []cleanupFile{{Name: "old-download", AgeMS: 2 * hour}, {Name: "new-download", AgeMS: 0}}, nil, "1.0.1"},
		{false, files, nil, []cleanupLease{{Name: "1.0.2", Directory: true}, {Name: "1.0.3", Directory: true, AgeMS: 8 * 24 * hour}}, "1.0.1"},
		{true, files, nil, []cleanupLease{{Name: "1.0.2", Active: true}, {Name: "1.0.3"}, {Name: "1.0.4", Malformed: true}, {Name: "legacy", Directory: true}}, "1.0.1"},
		{false, []cleanupFile{{Name: "only", Mode: 0755}}, nil, nil, ""},
	}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "profiles": profiles})
	fixture, _ := filepath.Abs("../../testdata/native-cli-cleanup.fixture.ts")
	command := exec.Command(bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "CC_HAHA_INSTALLATION_SOURCE_FIXTURE=1", "NODE_ENV=production", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	command.Stdin = bytes.NewReader(payload)
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	var source struct {
		Observations []cleanupObservation
		Blocked      []string
	}
	if err = json.Unmarshal(out, &source); err != nil || len(source.Observations) != len(profiles) || len(source.Blocked) > 0 {
		t.Fatal(err, string(out))
	}
	for i, profile := range profiles {
		target := filepath.Join(allocation, "go-home", string(rune('a'+i)))
		env := map[string]string{"ENABLE_PID_BASED_VERSION_LOCKING": "false"}
		if profile.PID {
			env["ENABLE_PID_BASED_VERSION_LOCKING"] = "true"
		}
		installer, err := New(Options{Home: target, Environment: env, Platform: "darwin", Architecture: "arm64"})
		if err != nil {
			t.Fatal(err)
		}
		dirs := installer.Directories()
		for _, dir := range []string{dirs.Versions, dirs.Staging, dirs.Locks, filepath.Dir(dirs.Executable)} {
			if err = os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
		}
		now := time.Now()
		stamp := func(path string, age int64) {
			t.Helper()
			at := now.Add(-time.Duration(age) * time.Millisecond)
			if err = os.Chtimes(path, at, at); err != nil {
				t.Fatal(err)
			}
		}
		for _, file := range profile.Files {
			path := filepath.Join(dirs.Versions, file.Name)
			if file.Directory {
				err = os.Mkdir(path, 0755)
			} else {
				data := []byte("fixture binary")
				if file.Empty {
					data = nil
				}
				err = os.WriteFile(path, data, os.FileMode(file.Mode))
			}
			if err != nil {
				t.Fatal(err)
			}
			stamp(path, file.AgeMS)
		}
		for _, file := range profile.Staging {
			path := filepath.Join(dirs.Staging, file.Name)
			if err = os.Mkdir(path, 0755); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(path, "download.part"), []byte("staged"), 0600); err != nil {
				t.Fatal(err)
			}
			stamp(path, file.AgeMS)
		}
		if profile.Symlink != "" {
			relative, _ := filepath.Rel(filepath.Dir(dirs.Executable), filepath.Join(dirs.Versions, profile.Symlink))
			if err = os.Symlink(relative, dirs.Executable); err != nil {
				t.Fatal(err)
			}
		}
		for _, lease := range profile.Locks {
			path := filepath.Join(dirs.Locks, lease.Name+".lock")
			if lease.Directory {
				err = os.Mkdir(path, 0755)
			} else {
				pid := 0
				if lease.Active {
					pid = os.Getpid()
				}
				data, _ := json.Marshal(pidLock{PID: pid, Version: lease.Name, Executable: os.Args[0], AcquiredAt: now.UnixMilli()})
				if lease.Malformed {
					data = []byte("{not json")
				}
				err = os.WriteFile(path, data, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			stamp(path, lease.AgeMS)
		}
		installer.CleanupOldVersions(context.Background())
		list := func(dir string) []string {
			t.Helper()
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			names := []string{}
			for _, entry := range entries {
				names = append(names, entry.Name())
			}
			sort.Strings(names)
			return names
		}
		got := cleanupObservation{Versions: list(dirs.Versions), Staging: list(dirs.Staging), Locks: list(dirs.Locks)}
		if link, err := os.Readlink(dirs.Executable); err == nil {
			got.Symlink = &link
		}
		if !reflect.DeepEqual(got, source.Observations[i]) {
			t.Fatalf("profile %d: Go %#v Source %#v", i, got, source.Observations[i])
		}
	}
}

func TestVersionMtimeLeaseCannotReleaseReplacement(t *testing.T) {
	installer, err := New(Options{Home: t.TempDir(), Platform: "darwin", Architecture: "arm64"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "1.2.3.lock")
	release, err := installer.acquireMtime(path)
	if err != nil {
		t.Fatal(err)
	}
	if next, err := installer.acquireMtime(path); next != nil || !os.IsExist(err) {
		t.Fatal("active lease replaced", err)
	}
	// Keep the original inode alive so the OS cannot reuse it for the replacement.
	original := path + ".original"
	if err = os.Rename(path, original); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(path, 0755); err != nil {
		t.Fatal(err)
	}
	release()
	release()
	if _, err = os.Stat(path); err != nil {
		t.Fatal("foreign lease removed", err)
	}
}
