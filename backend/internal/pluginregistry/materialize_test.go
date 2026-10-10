package pluginregistry

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/pluginzip"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func materializeSource(t *testing.T, home string, input any) json.RawMessage {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun not configured")
	}
	if err := os.MkdirAll(filepath.Join(home, "tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	_, file, _, _ := runtime.Caller(0)
	fixture := filepath.Join(filepath.Dir(file), "../../../scripts/quality-gate/go-parity/native-plugin-materialize.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Stdin = bytes.NewReader(raw(input))
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_PLUGIN_MATERIALIZE_FIXTURE=1", "NODE_ENV=production", "PATH=/usr/bin:/bin", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_ALLOW_PROTOCOL=file"}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Source %v: %s", err, stderr.Bytes())
	}
	return output
}
func snapshotTree(t *testing.T, root string) []map[string]any {
	t.Helper()
	rows := []map[string]any{}
	var walk func(string, string)
	walk = func(path, relative string) {
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		kind := "file"
		if info.IsDir() {
			kind = "directory"
		}
		if info.Mode()&os.ModeSymlink != 0 {
			kind = "link"
		}
		row := map[string]any{"path": relative, "type": kind, "mode": uint32(info.Mode().Perm())}
		if kind == "link" {
			value, err := os.Readlink(path)
			if err != nil {
				t.Fatal(err)
			}
			row["link"] = value
		} else if kind == "file" {
			value, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			row["bytes"] = base64.StdEncoding.EncodeToString(value)
		}
		rows = append(rows, row)
		if kind == "directory" {
			dir, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			entries, err := dir.ReadDir(-1)
			_ = dir.Close()
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				rel := entry.Name()
				if relative != "" {
					rel = relative + "/" + rel
				}
				walk(filepath.Join(path, entry.Name()), rel)
			}
		}
	}
	walk(root, "")
	return rows
}
func materializerForTest(t *testing.T, home string) *Materializer {
	t.Helper()
	m, err := NewMaterializer(MaterializerOptions{Directories: Directories{Primary: filepath.Join(home, ".claude", "plugins")}, CWD: home, Environment: map[string]string{"HOME": home, "USERPROFILE": home, "PATH": "/usr/bin:/bin", "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": "/dev/null", "GIT_ALLOW_PROTOCOL": "file"}, Now: func() time.Time { return time.UnixMilli(1780272000000) }, RandomFloat: func() float64 { return 0.1234 }})
	if err != nil {
		t.Fatal(err)
	}
	return m
}
func TestMaterializationPrimitivesAgainstProductionSource(t *testing.T) {
	if os.Getenv("CC_HAHA_SOURCE_BUN") == "" {
		t.Skip("pinned Source Bun not configured")
	}
	t.Run("directory-version-name", func(t *testing.T) {
		home := t.TempDir()
		m := materializerForTest(t, home)
		env := map[string]string{"CLAUDE_CODE_PLUGIN_CACHE_DIR": "~/cache-override", "CLAUDE_CODE_PLUGIN_SEED_DIR": filepath.Join(home, "first") + string(filepath.ListSeparator) + "~/second", "CLAUDE_CODE_USE_COWORK_PLUGINS": "true"}
		ops := []any{map[string]any{"type": "directories"}, map[string]any{"type": "versionedPath", "base": home, "id": "a😀@m.😀", "version": "1😀/../2"}, map[string]any{"type": "temporaryName", "source": map[string]any{"source": "git-subdir"}}, map[string]any{"type": "version", "id": "sample@mkt", "source": map[string]any{"source": "git-subdir", "path": `./plugins\sample///`}, "sha": "0123456789abcdef0123456789abcdef01234567"}, map[string]any{"type": "version", "id": "sample@mkt", "source": "./sample", "manifest": map[string]any{"version": "1.2.3"}, "provided": "2.0", "sha": "other"}}
		var expected struct{ Results []json.RawMessage }
		if err := json.Unmarshal(materializeSource(t, home, map[string]any{"now": m.options.Now().UnixMilli(), "random": 0.1234, "environment": env, "operations": ops}), &expected); err != nil {
			t.Fatal(err)
		}
		nativeDirs := ResolveDirectories(home, filepath.Join(home, ".claude"), false, func(k string) string { return env[k] })
		compare(t, map[string]any{"value": nativeDirs}, expected.Results[0])
		compare(t, map[string]any{"value": VersionedPathIn(home, "a😀@m.😀", "1😀/../2")}, expected.Results[1])
		name, err := m.TemporaryName(raw(map[string]any{"source": "git-subdir"}))
		if err != nil {
			t.Fatal(err)
		}
		compare(t, map[string]any{"value": name}, expected.Results[2])
		compare(t, map[string]any{"value": CalculateVersion(raw(map[string]any{"source": "git-subdir", "path": `./plugins\sample///`}), protocol.JSONObject{}, "", "", "0123456789abcdef0123456789abcdef01234567")}, expected.Results[3])
		compare(t, map[string]any{"value": CalculateVersion(raw("./sample"), protocol.NewJSONObject(field("version", "1.2.3")), "", "2.0", "other")}, expected.Results[4])
	})
	for _, kind := range []string{"copy", "copyVersioned", "cache"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			m := materializerForTest(t, home)
			sourcePath := filepath.Join(home, "source")
			target := filepath.Join(home, "target")
			put(t, filepath.Join(sourcePath, ".claude-plugin", "plugin.json"), `{"name":"sample","version":"1.2.3","future":"strip"}`)
			put(t, filepath.Join(sourcePath, "scripts", "run"), "#!/bin/sh\necho offline\n")
			if err := os.Chmod(filepath.Join(sourcePath, "scripts", "run"), 0751); err != nil {
				t.Fatal(err)
			}
			put(t, filepath.Join(home, "outside"), "Outside")
			if err := os.Symlink("scripts/run", filepath.Join(sourcePath, "internal")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("missing", filepath.Join(sourcePath, "broken")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("../outside", filepath.Join(sourcePath, "external")); err != nil {
				t.Fatal(err)
			}
			var operation map[string]any
			switch kind {
			case "copy":
				operation = map[string]any{"type": kind, "source": sourcePath, "target": target}
			case "copyVersioned":
				operation = map[string]any{"type": kind, "source": sourcePath, "id": "sample@mkt", "version": "1.2.3"}
				target = m.versioned("sample@mkt", "1.2.3")
			case "cache":
				operation = map[string]any{"type": kind, "source": sourcePath}
				target = filepath.Join(m.options.Directories.Primary, "cache", "sample")
			}
			var expected struct {
				Results []json.RawMessage
				Trees   []json.RawMessage
			}
			if err := json.Unmarshal(materializeSource(t, home, map[string]any{"now": m.options.Now().UnixMilli(), "random": 0.1234, "operations": []any{operation}, "inspect": []string{target}}), &expected); err != nil {
				t.Fatal(err)
			}
			// Restore the initially absent destination; do not let Source's cache
			// write satisfy the native producer before it has copied anything.
			if err := os.RemoveAll(m.options.Directories.Primary); err != nil {
				t.Fatal(err)
			}
			if kind == "copy" {
				if err := os.RemoveAll(target); err != nil {
					t.Fatal(err)
				}
			}
			var value any
			var err error
			switch kind {
			case "copy":
				err = CopyDirectory(context.Background(), sourcePath, target)
			case "copyVersioned":
				value, err = m.CopyToVersioned(context.Background(), sourcePath, "sample@mkt", "1.2.3", protocol.JSONObject{}, "")
			case "cache":
				value, err = m.Cache(context.Background(), raw(sourcePath), protocol.JSONObject{})
			}
			if err != nil {
				compare(t, map[string]any{"error": err.Error()}, expected.Results[0])
			} else {
				compare(t, map[string]any{"value": value}, expected.Results[0])
			}
			compare(t, snapshotTree(t, target), expected.Trees[0])
		})
	}
}

func TestInstalledSnapshotBackgroundUpdateAndScopeIsolationAgainstSource(t *testing.T) {
	if os.Getenv("CC_HAHA_SOURCE_BUN") == "" {
		t.Skip("pinned Source Bun not configured")
	}
	home := t.TempDir()
	dir := filepath.Join(home, ".claude", "plugins")
	initial := `{"version":2,"plugins":{"sample@mkt":[{"scope":"user","installPath":"/old/cache","version":"1","installedAt":"2026-01-01T00:00:00.000Z"}]}}`
	put(t, filepath.Join(dir, "installed_plugins.json"), initial)
	project := filepath.Join(home, "project")
	metadata := protocol.NewJSONObject(field("version", "3"), field("installPath", "/project/cache"), field("installedAt", "2026-01-01T00:00:00.000Z"))
	ops := []any{map[string]any{"type": "session"}, map[string]any{"type": "updateDisk", "id": "sample@mkt", "scope": "user", "path": "/new/cache", "version": "2"}, map[string]any{"type": "session"}, map[string]any{"type": "pending"}, map[string]any{"type": "addInstalled", "id": "sample@mkt", "scope": "project", "project": project, "metadata": metadata}, map[string]any{"type": "disk"}, map[string]any{"type": "removeInstalled", "id": "sample@mkt", "scope": "project", "project": project}, map[string]any{"type": "clearInstalled"}, map[string]any{"type": "session"}}
	var expected struct{ Results []json.RawMessage }
	if err := json.Unmarshal(materializeSource(t, home, map[string]any{"now": 1780272000000, "operations": ops}), &expected); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(dir, "installed_plugins.json"), initial)
	registry, err := NewInstalledRegistry(InstalledOptions{PluginsDir: dir, Store: storage.New(storage.Options{}), Now: func() time.Time { return time.UnixMilli(1780272000000) }})
	if err != nil {
		t.Fatal(err)
	}
	for i, op := range ops {
		operation := op.(map[string]any)
		var value any
		var err error
		switch operation["type"] {
		case "session":
			value, err = registry.Session(context.Background())
		case "updateDisk":
			err = registry.UpdateDisk(context.Background(), "sample@mkt", "user", nil, "/new/cache", "2", nil)
		case "pending":
			value, err = registry.Pending(context.Background())
		case "addInstalled":
			err = registry.Add(context.Background(), "sample@mkt", "project", "/project/cache", metadata, &project)
		case "disk":
			value, err = registry.Disk()
		case "removeInstalled":
			err = registry.Remove(context.Background(), "sample@mkt", "project", &project)
		case "clearInstalled":
			registry.Clear()
		}
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			if err != nil {
				t.Fatal(err)
			}
			compare(t, map[string]any{"value": value}, expected.Results[i])
		})
	}
}

func makeLocalPluginRepository(t *testing.T, home string) (string, string) {
	t.Helper()
	repo := filepath.Join(home, "repository")
	put(t, filepath.Join(repo, ".claude-plugin", "plugin.json"), `{"name":"sample","version":"2.0","settings":{"agent":"Plan"}}`)
	put(t, filepath.Join(repo, "skills", "sample", "SKILL.md"), "# Git fixture")
	env := []string{"HOME=" + home, "PATH=/usr/bin:/bin", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_DATE=2026-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z"}
	for _, args := range [][]string{{"init", "-q", repo}, {"-C", repo, "add", "."}, {"-C", repo, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "fixture"}} {
		cmd := exec.Command("/usr/bin/git", args...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fixture Git %v: %s", err, out)
		}
	}
	sha := ReadGitSHA(repo)
	if sha == "" {
		t.Fatal("fixture Git has no HEAD")
	}
	return repo, sha
}
func TestFullPluginLoaderUsesNativeMaterializationAgainstSource(t *testing.T) {
	if os.Getenv("CC_HAHA_SOURCE_BUN") == "" {
		t.Skip("pinned Source Bun not configured")
	}
	for _, scene := range []string{"local", "missing-local", "git", "pinned-git"} {
		t.Run(scene, func(t *testing.T) {
			f := newCacheFixture(t)
			m := materializerForTest(t, f.home)
			m.options.CWD = f.cwd
			f.options.Prepare = m.Prepare
			f.input["loadFull"] = true
			switch scene {
			case "local":
				put(t, filepath.Join(f.market, "plugins", "sample", ".claude-plugin", "plugin.json"), `{"name":"sample","version":"1.2.3","settings":{"agent":"Plan"}}`)
			case "missing-local":
				if err := os.RemoveAll(filepath.Join(f.market, "plugins", "sample")); err != nil {
					t.Fatal(err)
				}
			default:
				repo, sha := makeLocalPluginRepository(t, f.home)
				uri := (&url.URL{Scheme: "file", Path: repo}).String()
				source := map[string]any{"source": "url", "url": uri}
				if scene == "pinned-git" {
					source["sha"] = sha
				}
				f.catalog(t, []any{map[string]any{"name": "sample", "source": source}})
			}
			var expected struct {
				Cache          json.RawMessage
				PluginSettings json.RawMessage
			}
			if err := json.Unmarshal(source(t, f.home, f.input), &expected); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(filepath.Join(f.home, ".claude", "plugins", "cache")); err != nil {
				t.Fatal(err)
			}
			loader, err := New(f.options)
			if err != nil {
				t.Fatal(err)
			}
			result, err := loader.LoadFull(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			compare(t, result, expected.Cache)
			if scene == "git" || scene == "pinned-git" {
				if len(result.Enabled) != 1 || !strings.Contains(result.Enabled[0].Path, "/cache/mkt/sample/") {
					t.Fatalf("not materialized: %+v", result)
				}
				if _, err := os.Stat(filepath.Join(result.Enabled[0].Path, ".git")); !os.IsNotExist(err) {
					t.Fatal("version cache retained clone metadata")
				}
			}
			var base any
			if f.base != nil && len(f.base.OwnProperties()) > 0 {
				base = f.base
			}
			compare(t, base, expected.PluginSettings)
		})
	}
}

func TestNativeInstallScopeRegistrationAgainstActualSource(t *testing.T) {
	if os.Getenv("CC_HAHA_SOURCE_BUN") == "" {
		t.Skip("pinned Source Bun not configured")
	}
	for _, id := range []string{"sample@mkt", "sample@sample"} {
		t.Run(id, func(t *testing.T) {
			home := t.TempDir()
			m := materializerForTest(t, home)
			source := filepath.Join(home, "source")
			put(t, filepath.Join(source, ".claude-plugin", "plugin.json"), `{"name":"sample","version":"1.2.3"}`)
			put(t, filepath.Join(source, "skills", "SKILL.md"), "# Fixture")
			entry := protocol.NewJSONObject(field("name", "sample"), field("source", "./source"))
			project := filepath.Join(home, "project")
			ops := []any{map[string]any{"type": "installAndRegister", "id": id, "entry": entry, "scope": "project", "project": project, "localSource": source}, map[string]any{"type": "disk"}}
			path := m.versioned(id, "1.2.3")
			var expected struct {
				Results []json.RawMessage
				Trees   []json.RawMessage
			}
			if err := json.Unmarshal(materializeSource(t, home, map[string]any{"now": m.options.Now().UnixMilli(), "random": 0.1234, "operations": ops, "inspect": []string{path}}), &expected); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(m.options.Directories.Primary); err != nil {
				t.Fatal(err)
			}
			registry, err := NewInstalledRegistry(InstalledOptions{PluginsDir: m.options.Directories.Primary, Store: storage.New(storage.Options{})})
			if err != nil {
				t.Fatal(err)
			}
			m.options.Installed = registry
			result, err := m.InstallAndRegister(context.Background(), id, entry, "project", &project, source)
			if err != nil {
				t.Fatal(err)
			}
			compare(t, map[string]any{"value": result}, expected.Results[0])
			data, err := registry.Disk()
			if err != nil {
				t.Fatal(err)
			}
			compare(t, map[string]any{"value": data}, expected.Results[1])
			compare(t, snapshotTree(t, path), expected.Trees[0])
		})
	}
}

func TestZIPMaterializerAndCachedLoaderUseOneNativeArchive(t *testing.T) {
	f := newCacheFixture(t)
	temp := filepath.Join(f.home, "tmp")
	if err := os.MkdirAll(temp, 0700); err != nil {
		t.Fatal(err)
	}
	archive, err := pluginzip.New(pluginzip.Options{TempDir: temp, Home: f.home, Environment: map[string]string{"CLAUDE_CODE_PLUGIN_USE_ZIP_CACHE": "true"}})
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Cleanup(context.Background())
	m := materializerForTest(t, f.home)
	m.options.CWD = f.cwd
	m.options.Archive = archive
	m.options.Environment["CLAUDE_CODE_PLUGIN_USE_ZIP_CACHE"] = "true"
	f.options.Prepare = m.Prepare
	f.options.ExtractZip = archive.ExtractPlugin
	f.options.Environment = func(k string) string { return m.options.Environment[k] }
	put(t, filepath.Join(f.market, "plugins", "sample", ".claude-plugin", "plugin.json"), `{"name":"sample","version":"1.2.3"}`)
	loader, err := New(f.options)
	if err != nil {
		t.Fatal(err)
	}
	full, err := loader.LoadFull(context.Background())
	if err != nil || len(full.Enabled) != 1 {
		t.Fatalf("full ZIP loading=%+v %v", full, err)
	}
	zip := m.versioned("sample@mkt", "1.2.3") + ".zip"
	if _, err := os.Stat(zip); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(strings.TrimSuffix(zip, ".zip")); !os.IsNotExist(err) {
		t.Fatal("ZIP conversion retained the source directory")
	}
	if _, err := os.Stat(filepath.Join(full.Enabled[0].Path, "skills", "sample", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	// External cache-only loading selects the recorded ZIP and the same native
	// extraction service. Full materialization has already proved its producer.
	f.catalog(t, []any{map[string]any{"name": "sample", "source": map[string]any{"source": "github", "repo": "fixture/sample"}}})
	f.installed(t, map[string]any{"sample@mkt": []any{installEntry("user", zip, "")}})
	f.options.Installed.Clear()
	loader.Clear()
	cached, err := loader.LoadCached(context.Background())
	if err != nil || len(cached.Enabled) != 1 {
		t.Fatalf("cached ZIP=%+v %v", cached, err)
	}
	if cached.Enabled[0].Path != full.Enabled[0].Path {
		t.Fatal("cached/full consumers used different extraction roots")
	}
}
