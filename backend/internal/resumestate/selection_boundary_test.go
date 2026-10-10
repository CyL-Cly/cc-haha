package resumestate

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestOriginalSelectorsKeepFaultOwnershipAndRejectUnsupportedIdentity(t *testing.T) {
	for _, mode := range []string{"SID", "JSONL", "lite-file", "date", "object-tip", "lone-SID"} {
		t.Run(mode, func(t *testing.T) {
			home := t.TempDir()
			files := &faultFiles{}
			options, events := testOptions(t, home, files)
			path := filepath.Join(home, "session.jsonl")
			input := Input{Source: quote(oldSID)}
			if mode == "SID" {
				path = filepath.Join(home, ".claude", "projects", sanitizePath(home), oldSID+".jsonl")
			}
			if mode == "JSONL" {
				input.JSONLPath = path
			}
			if mode == "lite-file" {
				input = original([]json.RawMessage{}, protocol.JSONField{Name: "sessionId", Value: quote(oldSID)}, protocol.JSONField{Name: "fullPath", Value: quote(path)})
			}
			if mode == "date" || mode == "object-tip" {
				path = filepath.Join(home, ".claude", "projects", sanitizePath(home), oldSID+".jsonl")
			}
			raw := string(plain("u", "protected original")) + "\n"
			if mode == "date" {
				raw = strings.Replace(raw, "2025-01-01T00:00:00Z", "unported legacy date", 1)
			}
			if mode == "object-tip" {
				raw = strings.Replace(raw, `"uuid":"u"`, `"uuid":{"original":true}`, 1)
			}
			writePrivate(t, path, raw)
			if mode == "SID" || mode == "JSONL" || mode == "lite-file" {
				files.openError = fs.ErrPermission
				files.readPath, files.readError = path, fs.ErrPermission
			}
			if mode == "lone-SID" {
				input = Input{Source: []byte(`"\ud800"`)}
			}
			service, err := New(options)
			if err != nil {
				t.Fatal(err)
			}
			result, err := service.Load(input)
			if mode == "SID" || mode == "JSONL" || mode == "lite-file" {
				if err != nil || len(events.errors) != 0 || mode == "SID" && result != nil || mode != "SID" && (result == nil || len(result.Messages) != 0) {
					t.Fatal("Source-owned read failure no longer yields empty original history", err)
				}
				if data, err := os.ReadFile(path); err != nil || string(data) != raw {
					t.Fatal("failed read modified original bytes")
				}
				return
			}
			if result != nil || err == nil || len(events.errors) != 1 {
				t.Fatal("selection fault proceeded or changed error ownership", err)
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil || string(data) != raw || len(events.phases) != 0 {
				t.Fatal("selector modified original or started state restoration")
			}
		})
	}
}

func TestSIDCachePrimesOnceFromOriginalFileAndStateBytesAreCloned(t *testing.T) {
	home := t.TempDir()
	options, _ := testOptions(t, home, OSFiles{})
	service, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(service.projectDir(), oldSID+".jsonl")
	writePrivate(t, path, string(plain("original", "q"))+"\n")
	if _, err := service.Load(Input{Source: quote(oldSID)}); err != nil {
		t.Fatal(err)
	}
	observed := service.State()
	observed.SessionUUIDs[oldSID][0][0] = 'x'
	writePrivate(t, path, string(plain("rewritten", "q"))+"\n")
	if _, err := service.Load(Input{Source: quote(oldSID)}); err != nil {
		t.Fatal(err)
	}
	uuids := service.State().SessionUUIDs[oldSID]
	if len(uuids) != 1 || text(uuids[0]) != "original" {
		t.Fatal("getLastSessionLog cache was overwritten by later file or observer")
	}
}

func TestResumeRevalidatesOriginalCachedPolicyBeforeAnyEffect(t *testing.T) {
	home := t.TempDir()
	options, events := testOptions(t, home, OSFiles{})
	service, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"merged", "policy"} {
		valid := protocol.NewJSONObject()
		options.SettingsState.SetSessionSettingsCache(&clisettings.ParsedSettings{Settings: &valid})
		options.SettingsState.SetCachedSettingsForSource("policySettings", &valid)
		invalid := object([]byte(`{"hooks":3}`))
		if field == "merged" {
			options.SettingsState.SetSessionSettingsCache(&clisettings.ParsedSettings{Settings: &invalid})
		} else {
			options.SettingsState.SetCachedSettingsForSource("policySettings", &invalid)
		}
		if _, err := service.Load(original([]json.RawMessage{plain("u", "q")})); err == nil {
			t.Fatal("mutated cached policy bypassed preflight")
		}
		if len(events.phases) != 0 || len(service.State().PlanSlugs) != 0 {
			t.Fatal("unsupported new snapshot ran restore")
		}
	}
	valid := protocol.NewJSONObject()
	options.SettingsState.SetSessionSettingsCache(&clisettings.ParsedSettings{Settings: &valid})
	options.SettingsState.SetCachedSettingsForSource("policySettings", &valid)
	options.Environment["CLAUDE_CODE_SIMPLE"] = "false"
	options.Arguments = []string{"--plugin-dir=private"}
	service.options.Arguments = options.Arguments
	if _, err := service.Load(original([]json.RawMessage{plain("u", "q")})); err == nil {
		t.Fatal("late unsupported plugin flag escaped")
	}
}

func TestContinueIgnoresNonfilesAndUsesOriginalBirthTieBreak(t *testing.T) {
	home := t.TempDir()
	options, _ := testOptions(t, home, OSFiles{})
	service, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	dir := service.projectDir()
	writePrivate(t, filepath.Join(dir, "README.txt"), "protected")
	if err := os.MkdirAll(filepath.Join(dir, "dddddddd-eeee-ffff-aaaa-bbbbbbbbbbbb.jsonl"), 0700); err != nil {
		t.Fatal(err)
	}
	newer := "cccccccc-dddd-eeee-ffff-aaaaaaaaaaaa"
	writePrivate(t, filepath.Join(dir, oldSID+".jsonl"), string(plain("older", "q"))+"\n")
	message := strings.Replace(string(plain("newer", "q")), oldSID, newer, 1)
	writePrivate(t, filepath.Join(dir, newer+".jsonl"), message+"\n")
	service.options.Stat = func(path string) (Stat, error) {
		info, err := os.Stat(path)
		birth := float64(1)
		if strings.HasPrefix(filepath.Base(path), newer) {
			birth = 2
		}
		return Stat{Modified: 10, Birth: birth, Size: info.Size()}, err
	}
	result, err := service.Load(Input{})
	if err != nil || result == nil || text(result.Metadata.Get("sessionId")) != newer {
		t.Fatal("birth tie chose unrelated or older original", err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "README.txt")); err != nil || string(data) != "protected" {
		t.Fatal("ignored original changed")
	}
}

func TestNoVisibleContinueAndBareArgumentKeepGenuineHookDisable(t *testing.T) {
	home := t.TempDir()
	options, _ := testOptions(t, home, OSFiles{})
	options.Environment["CLAUDE_CODE_SIMPLE"] = "false"
	options.Arguments = []string{"--bare"}
	service, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	writePrivate(t, filepath.Join(service.projectDir(), oldSID+".jsonl"), strings.Replace(string(plain("u", "q")), `"type":"user"`, `"isSidechain":true,"type":"user"`, 1)+"\n")
	if result, err := service.Load(Input{}); err != nil || result != nil {
		t.Fatal("no visible session resumed", err)
	}
	if _, err := service.Load(original([]json.RawMessage{plain("u", "q")})); err != nil {
		t.Fatal("real --bare path did not disable hooks", err)
	}
	var nilResult *Result
	if data, err := nilResult.MarshalJSON(); err != nil || string(data) != "null" {
		t.Fatal("missing resume result cannot be represented")
	}
}

func TestPluginRegistryNativeReadFailuresRemainExplicit(t *testing.T) {
	for _, mode := range []string{"invalid", "permission", "marketplace"} {
		t.Run(mode, func(t *testing.T) {
			home := t.TempDir()
			files := &faultFiles{}
			options, events := testOptions(t, home, files)
			options.Environment["CLAUDE_CODE_SIMPLE"] = "false"
			path := filepath.Join(home, ".claude", "plugins", "installed_plugins.json")
			raw := "{invalid-original"
			if mode == "marketplace" {
				path = filepath.Join(home, ".claude", "plugins", "known_marketplaces.json")
				raw = `{"fixture":{"source":{}}}`
			}
			writePrivate(t, path, raw)
			if mode == "permission" {
				files.readPath, files.readError = path, fs.ErrPermission
			}
			service, err := New(options)
			if err != nil {
				t.Fatal(err)
			}
			result, err := service.Load(original([]json.RawMessage{plain("u", "q")}))
			if result != nil || err == nil || len(events.errors) != 1 {
				t.Fatal("unported registry silently continued", err)
			}
			if mode == "permission" && !errors.Is(err, fs.ErrPermission) {
				t.Fatal("original native cause lost")
			}
			if bytes, err := os.ReadFile(path); err != nil || string(bytes) != raw {
				t.Fatal("registry recovery wrote private original")
			}
		})
	}
}
