//go:build native_process_fixture && !offline_native_fixture

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNativeFixturePhysicalRootAliasesMissingTailAndSymlinkEscapes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("actual symbolic directory alias requires POSIX fixture")
	}
	root := t.TempDir()
	for _, name := range []string{"home", "tmp", "project"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(t.TempDir(), "actual-private-alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	environment := func() map[string]string {
		return map[string]string{"CC_HAHA_NATIVE_FIXTURE_ROOT": alias, "HOME": filepath.Join(alias, "home"), "TMPDIR": filepath.Join(alias, "tmp"), "CLAUDE_CONFIG_DIR": filepath.Join(alias, "not-yet-created", "config"), "USER_TYPE": "external"}
	}
	t.Run("actual-alias-and-inside-missing-tail", func(t *testing.T) {
		env := environment()
		native, err := newNativeBoundaryForEnvironment(env, filepath.Join(alias, "project"), runtime.GOOS)
		if err != nil {
			t.Fatal("actual same private tree rejected", err)
		}
		if native.Environment["HOME"] != env["HOME"] || native.Environment["CLAUDE_CONFIG_DIR"] != env["CLAUDE_CONFIG_DIR"] {
			t.Fatal("physical fixture guard rewrote raw launch strings")
		}
	})
	outside := t.TempDir()
	link := filepath.Join(root, "project", "outside")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, key, path string }{
		{"config-existing-symlink-outside", "CLAUDE_CONFIG_DIR", link},
		{"config-missing-tail-through-symlink-outside", "CLAUDE_CONFIG_DIR", filepath.Join(link, "missing", "config")},
		{"home-symlink-outside", "HOME", link},
		{"tmp-missing-tail-through-symlink-outside", "TMPDIR", filepath.Join(link, "missing")},
		{"config-lexically-outside-missing-tail", "CLAUDE_CONFIG_DIR", filepath.Join(outside, "missing", "config")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := environment()
			// Use the real spelling here so a root-alias failure cannot mask the
			// tested escape. Each target still belongs to a disposable fixture.
			env["CC_HAHA_NATIVE_FIXTURE_ROOT"], env["HOME"], env["TMPDIR"] = root, filepath.Join(root, "home"), filepath.Join(root, "tmp")
			env[tc.key] = tc.path
			if _, err := newNativeBoundaryForEnvironment(env, filepath.Join(root, "project"), runtime.GOOS); err == nil {
				t.Fatal("actual filesystem escape accepted", tc.key, tc.path)
			}
		})
	}
}
