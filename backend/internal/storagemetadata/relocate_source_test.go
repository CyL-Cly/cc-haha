package storagemetadata

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func put(t *testing.T, root, path string, value any) {
	t.Helper()
	file := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	var data []byte
	if text, ok := value.(string); ok {
		data = []byte(text)
	} else {
		data = encode(value)
	}
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
}
func copyTree(t *testing.T, source, target string) {
	t.Helper()
	if err := os.MkdirAll(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := filepath.WalkDir(source, func(path string, entry os.DirEntry, failure error) error {
		if failure != nil {
			return failure
		}
		rel, _ := filepath.Rel(source, path)
		dest := filepath.Join(target, rel)
		if entry.IsDir() {
			return os.MkdirAll(dest, 0700)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dest, data, 0600)
	}); err != nil {
		t.Fatal(err)
	}
}
func filesIn(t *testing.T, stage string) map[string][]byte {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(stage)
	if err != nil {
		t.Fatal(err)
	}
	stage = canonical
	files := map[string][]byte{}
	if err := filepath.WalkDir(stage, func(path string, entry os.DirEntry, failure error) error {
		if failure != nil {
			return failure
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		rel, _ := filepath.Rel(stage, path)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = data
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return files
}

func TestActualSourceManagedMetadataRelocationOldFixturesAndPhysicalFaults(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	scenarios := []string{"full", "alias", "missing-source", "cowork-v1", "unsupported-relocation", "overlap", "bad-connectors-version", "bad-connectors", "bad-record", "bad-installation", "bad-directory", "bad-command", "bad-args", "bad-env", "bad-account", "bad-market", "bad-market-path", "bad-local-path", "bad-plugin-version", "bad-plugin-records", "bad-plugin-entry", "bad-plugin-path", "linked-file", "linked-directory", "hardlink", "bad-json", "no-metadata", "invalid-roots", "relative-root", "linked-skill", "unreadable", "no-search", "cannot-mkdir", "cannot-temp", "cc-not-directory", "no-installation", "no-schema", "infer-version", "fallback-version"}
	for _, scenario := range scenarios {
		t.Run(scenario, func(t *testing.T) {
			home, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			source, target, stageGo, stageSource := filepath.Join(home, "source"), filepath.Join(home, "target"), filepath.Join(home, "stage-go"), filepath.Join(home, "stage-source")
			if err = os.MkdirAll(source, 0700); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(source, "connectors", "runtime", "dingtalk", "1.0.50-win32-x64")
			cache := filepath.Join(source, "plugins", "cache", "haha-connectors", "office-dingtalk", "1.0.50-connector.1")
			state := map[string]any{"schemaVersion": 0, "futureState": true, "connectors": map[string]any{"dingtalk": map[string]any{"enabled": true, "installedVersion": "1.0.50", "futureRecord": "keep", "installation": map[string]any{"directory": dir, "command": filepath.Join(dir, "dws.exe"), "args": []string{filepath.Join(source, "connectors", "runtime", "prefix"), "--keep"}, "env": map[string]any{"DWS_CONFIG_DIR": filepath.Join(source, "connectors", "accounts", "dingtalk", "dws"), "DWS_KEYCHAIN_DIR": filepath.Join(source, "connectors", "accounts", "dingtalk", "keychain"), "DWS_DISABLE_KEYCHAIN": "1", "UNKNOWN_FUTURE": map[string]bool{"preserve": true}}, "unknownInstall": true}}}}
			put(t, source, "connectors/state.json", state)
			put(t, source, "cc-haha/storage-relocations.json", map[string]any{"version": 1, "previousRoots": []string{filepath.Join(home, "older"), source}, "futureField": map[string]bool{"keep": true}})
			put(t, source, "plugins/known_marketplaces.json", map[string]any{"haha-connectors": map[string]any{"installLocation": filepath.Join(source, "connectors", "marketplace"), "source": map[string]any{"source": "directory", "path": filepath.Join(source, "connectors", "marketplace"), "unknown": true}, "extra": true}, "external": map[string]any{"installLocation": filepath.Join(home, "external.json"), "source": map[string]any{"source": "file", "path": filepath.Join(home, "external.json")}}})
			put(t, source, "plugins/installed_plugins.json", map[string]any{"version": 2, "future": true, "plugins": map[string]any{"office-dingtalk@haha-connectors": []map[string]any{{"installPath": cache, "projectPath": "/external/project", "unknown": "keep"}}}})
			put(t, source, "plugins/cache/haha-connectors/office-dingtalk/1.0.50-connector.1/skills/office-dingtalk/SKILL.md", "old generated skill")
			put(t, source, "connectors/marketplace/plugins/office-dingtalk/skills/office-dingtalk/SKILL.md", "old generated skill")
			put(t, source, "settings.json", `{"futureShared":true}`)
			put(t, source, "projects/repo/session.jsonl", `{"type":"user","message":{"content":"old path text"}}`+"\n")
			records := state["connectors"].(map[string]any)
			record := records["dingtalk"].(map[string]any)
			install := record["installation"].(map[string]any)
			switch scenario {
			case "no-installation":
				record["installation"] = nil
			case "no-schema":
				delete(state, "schemaVersion")
			case "infer-version":
				delete(record, "installedVersion")
			case "fallback-version":
				delete(record, "installedVersion")
				install["directory"] = filepath.Join(source, "connectors", "runtime", "unknown")
			case "bad-connectors-version":
				state["schemaVersion"] = 4
			case "bad-connectors":
				state["connectors"] = nil
			case "bad-record":
				records["dingtalk"] = []any{}
			case "bad-installation":
				record["installation"] = "invalid"
			case "bad-directory":
				install["directory"] = false
			case "bad-command":
				install["command"] = nil
			case "bad-args":
				install["args"] = []any{1}
			case "bad-env":
				install["env"] = nil
			case "bad-account":
				install["env"].(map[string]any)["DWS_CONFIG_DIR"] = false
			case "no-metadata":
				if err = os.RemoveAll(filepath.Join(source, "cc-haha")); err != nil {
					t.Fatal(err)
				}
				if err = os.RemoveAll(filepath.Join(source, "connectors")); err != nil {
					t.Fatal(err)
				}
				if err = os.RemoveAll(filepath.Join(source, "plugins")); err != nil {
					t.Fatal(err)
				}
			case "invalid-roots":
				put(t, source, "cc-haha/storage-relocations.json", map[string]any{"version": 1, "previousRoots": nil})
			case "relative-root":
				put(t, source, "cc-haha/storage-relocations.json", map[string]any{"version": 1, "previousRoots": []string{"relative"}})
			case "unsupported-relocation":
				put(t, source, "cc-haha/storage-relocations.json", map[string]any{"version": 2, "previousRoots": []string{}})
			case "overlap":
				put(t, source, "cc-haha/storage-relocations.json", map[string]any{"version": 1, "previousRoots": []string{filepath.Join(target, "nested")}})
			case "bad-market":
				put(t, source, "plugins/known_marketplaces.json", map[string]any{"market": nil})
			case "bad-market-path":
				put(t, source, "plugins/known_marketplaces.json", map[string]any{"market": map[string]any{"installLocation": 4}})
			case "bad-local-path":
				put(t, source, "plugins/known_marketplaces.json", map[string]any{"market": map[string]any{"installLocation": dir, "source": map[string]any{"source": "file", "path": 4}}})
			case "bad-plugin-version":
				put(t, source, "plugins/installed_plugins.json", map[string]any{"version": 4})
			case "bad-plugin-records":
				put(t, source, "plugins/installed_plugins.json", map[string]any{"version": 2, "plugins": nil})
			case "bad-plugin-entry":
				put(t, source, "plugins/installed_plugins.json", map[string]any{"plugins": map[string]any{"id": []any{nil}}})
			case "bad-plugin-path":
				put(t, source, "plugins/installed_plugins.json", map[string]any{"plugins": map[string]any{"id": map[string]any{"installPath": 4}}})
			case "cowork-v1":
				put(t, source, "cowork_plugins/installed_plugins.json", map[string]any{"version": 1, "plugins": map[string]any{"external@market": map[string]any{"installPath": filepath.Join(source, "cowork_plugins", "cache", "external")}}})
			}
			if scenario != "no-metadata" {
				put(t, source, "connectors/state.json", state)
			}
			copyTree(t, source, stageGo)
			copyTree(t, source, stageSource)
			original := filesIn(t, source)
			if scenario == "alias" {
				alias := filepath.Join(home, "source-alias")
				if err = os.Symlink(source, alias); err != nil {
					t.Fatal(err)
				}
				source = alias
			}
			if scenario == "missing-source" {
				if err = os.RemoveAll(source); err != nil {
					t.Fatal(err)
				}
			}
			permissions := map[string]uint32{}
			for _, stage := range []string{stageGo, stageSource} {
				switch scenario {
				case "linked-file", "hardlink":
					outside := filepath.Join(home, "outside-"+filepath.Base(stage)+".json")
					put(t, home, filepath.Base(outside), `{"connectors":{}}`)
					file := filepath.Join(stage, "connectors", "state.json")
					if err = os.Remove(file); err != nil {
						t.Fatal(err)
					}
					if scenario == "hardlink" {
						err = os.Link(outside, file)
					} else {
						err = os.Symlink(outside, file)
					}
					if err != nil {
						t.Fatal(err)
					}
				case "linked-directory":
					old := filepath.Join(stage, "connectors")
					outside := filepath.Join(home, "outside-"+filepath.Base(stage))
					if err = os.Rename(old, outside); err != nil {
						t.Fatal(err)
					}
					if err = os.Symlink(outside, old); err != nil {
						t.Fatal(err)
					}
				case "bad-json":
					put(t, stage, "connectors/state.json", "{invalid-json")
				case "linked-skill":
					file := filepath.Join(stage, "connectors/marketplace/plugins/office-dingtalk/skills/office-dingtalk/SKILL.md")
					outside := filepath.Join(home, "skill-"+filepath.Base(stage))
					put(t, home, filepath.Base(outside), "protected skill")
					if err = os.Remove(file); err != nil {
						t.Fatal(err)
					}
					if err = os.Symlink(outside, file); err != nil {
						t.Fatal(err)
					}
				case "unreadable", "no-search":
					file := filepath.Join(stage, "cc-haha", "storage-relocations.json")
					mode := os.FileMode(0600)
					if scenario == "no-search" {
						file = filepath.Join(stage, "cc-haha")
						mode = 0700
					}
					if err = os.Chmod(file, 0); err != nil {
						t.Fatal(err)
					}
					permissions[file] = uint32(mode)
				case "cannot-mkdir", "cannot-temp":
					if err = os.Remove(filepath.Join(stage, "cc-haha", "storage-relocations.json")); err != nil {
						t.Fatal(err)
					}
					file := filepath.Join(stage, "cc-haha")
					if scenario == "cannot-mkdir" {
						if err = os.Remove(file); err != nil {
							t.Fatal(err)
						}
						file = stage
					}
					if err = os.Chmod(file, 0500); err != nil {
						t.Fatal(err)
					}
					permissions[file] = 0700
				case "cc-not-directory":
					if err = os.RemoveAll(filepath.Join(stage, "cc-haha")); err != nil {
						t.Fatal(err)
					}
					put(t, stage, "cc-haha", "not a directory")
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			goErr := RelocateManagedData(ctx, source, target, stageGo)
			for path, mode := range permissions {
				if inside(stageGo, path) {
					if err = os.Chmod(path, os.FileMode(mode)); err != nil {
						t.Fatal(err)
					}
				}
			}
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/storage-migration-metadata.fixture.ts")
			command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			command.Dir = home
			command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_STORAGE_METADATA_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
			command.Stdin = bytes.NewReader(encode(map[string]any{"homeDir": home, "sourceDir": source, "targetDir": target, "stagedDir": stageSource, "restorePermissions": permissions}))
			data, err := command.CombinedOutput()
			if err != nil {
				t.Fatal(err, string(data))
			}
			var expected struct {
				Files map[string][]byte
				Error string
			}
			if err = json.Unmarshal(data, &expected); err != nil {
				t.Fatal(err, string(data))
			}
			if (goErr == nil) != (expected.Error == "") {
				t.Fatal("Go/Source failure mismatch", goErr, expected.Error)
			}
			if scenario != "bad-json" && scenario != "unreadable" && scenario != "no-search" && scenario != "cannot-mkdir" && scenario != "cannot-temp" && scenario != "cc-not-directory" && goErr != nil && goErr.Error() != expected.Error {
				t.Fatal("Go/Source error mismatch", goErr, expected.Error)
			}
			actual := filesIn(t, stageGo)
			if !reflect.DeepEqual(actual, expected.Files) {
				t.Errorf("staged file inventory or bytes differ from actual Source")
				for path, want := range expected.Files {
					if !bytes.Equal(actual[path], want) {
						t.Errorf("file %s: Go %s Source %s", path, actual[path], want)
					}
				}
			}
			if scenario != "missing-source" {
				if !reflect.DeepEqual(filesIn(t, source), original) {
					t.Fatal("modified source bytes")
				}
			}
			for _, stage := range []string{stageGo, stageSource} {
				outside := filepath.Join(home, "outside-"+filepath.Base(stage)+".json")
				if content, err := os.ReadFile(outside); err == nil && string(content) != `{"connectors":{}}` {
					t.Fatal("changed outside linked metadata")
				}
			}
		})
	}
}
func TestWorkerExplicitStageAndAtomicWriteErrors(t *testing.T) {
	root := t.TempDir()
	source, target := filepath.Join(root, "source"), filepath.Join(root, "target")
	stage := filepath.Join(target, ".cc-haha-migration-real")
	if err := os.MkdirAll(stage, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	request := Request{source, target, stage}
	var output bytes.Buffer
	if err := RunWorker(context.Background(), bytes.NewReader(encode(request)), &output); err != nil || !strings.Contains(output.String(), `"relocated":true`) {
		t.Fatal(err, output.String())
	}

	invalid := []string{`{`, `{"sourceDir":"relative","targetDir":"/tmp","stagedDir":"/tmp/stage"}`, string(encode(Request{source, target, filepath.Join(root, "other-stage")})), string(encode(request)) + ` {}`, `{"unknown":true}`, string(encode(Request{source, source, stage}))}
	for _, raw := range invalid {
		output.Reset()
		if err := RunWorker(context.Background(), strings.NewReader(raw), &output); err == nil || output.Len() != 0 {
			t.Fatal("invalid worker request accepted", raw, err, output.String())
		}
	}
	for _, stageKind := range []string{"missing", "file", "link"} {
		bad := filepath.Join(target, ".cc-haha-migration-"+stageKind)
		if stageKind == "file" {
			put(t, target, filepath.Base(bad), "protected file")
		}
		if stageKind == "link" {
			if err := os.Symlink(stage, bad); err != nil {
				t.Fatal(err)
			}
		}
		output.Reset()
		if err := RunWorker(context.Background(), bytes.NewReader(encode(Request{source, target, bad})), &output); err == nil || output.Len() != 0 {
			t.Fatal("invalid stage accepted", stageKind, err)
		}
	}
	output.Reset()
	if err := RunWorker(context.Background(), strings.NewReader(string(encode(request))+strings.Repeat(" ", 64*1024)), &output); err == nil || output.Len() != 0 {
		t.Fatal("oversized request accepted", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := RelocateManagedData(cancelled, source, target, stage); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
