package resumestate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionresume"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

const oldSID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
const tempSID = "bbbbbbbb-cccc-dddd-eeee-ffffffffffff"

type memoryEvents struct {
	mu                               sync.Mutex
	allocations, diagnostics, errors []json.RawMessage
	phases                           []string
}

func (events *memoryEvents) push(rows *[]json.RawMessage, value any) {
	events.mu.Lock()
	defer events.mu.Unlock()
	*rows = append(*rows, encode(value))
}
func (events *memoryEvents) now() time.Time {
	events.push(&events.allocations, map[string]any{"kind": "time", "value": "2023-11-14T22:13:20.000Z"})
	return time.UnixMilli(1700000000000)
}
func (events *memoryEvents) millis() int64 {
	events.push(&events.allocations, map[string]any{"kind": "now", "value": int64(1700000000000)})
	return 1700000000000
}
func (events *memoryEvents) diagnostic(event sessionresume.Diagnostic) {
	value := protocol.NewJSONObject(protocol.JSONField{Name: "kind", Value: quote(event.Kind)}, protocol.JSONField{Name: "name", Value: quote(event.Name)}, protocol.JSONField{Name: "fields", Value: event.Fields})
	events.push(&events.diagnostics, value)
}
func errorRow(err error) json.RawMessage {
	var typed *sessionresume.Failure
	if errors.As(err, &typed) {
		return encode(typed)
	}
	message := err.Error()
	var native *os.PathError
	if errors.As(err, &native) {
		message = strings.TrimPrefix(storage.JavaScriptErrorText(err), "Error: ")
	}
	return encode(map[string]any{"name": "Error", "message": message})
}
func (events *memoryEvents) onError(err error) {
	events.now()
	events.push(&events.errors, errorRow(err))
}
func writePrivate(t *testing.T, path, stringValue string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(stringValue), 0600); err != nil {
		t.Fatal(err)
	}
}
func testOptions(t *testing.T, home string, files Files) (Options, *memoryEvents) {
	t.Helper()
	events := &memoryEvents{allocations: []json.RawMessage{}, diagnostics: []json.RawMessage{}, errors: []json.RawMessage{}, phases: []string{}}
	calls := 0
	var mu sync.Mutex
	uuid := func() string {
		mu.Lock()
		defer mu.Unlock()
		calls++
		value := fmt.Sprintf("00000000-0000-4000-8000-%012d", calls)
		events.push(&events.allocations, map[string]any{"kind": "uuid", "value": value})
		return value
	}
	factory, err := messages.NewFactory(uuid, events.now)
	if err != nil {
		t.Fatal(err)
	}
	reducer, err := sessionresume.New(sessionresume.Options{Factory: factory, TranscriptClassifier: true, RelativePath: func(raw json.RawMessage) (json.RawMessage, error) { return sessionresume.RelativePath(home, home, raw) }, OnDiagnostic: events.diagnostic, OnError: events.onError})
	if err != nil {
		t.Fatal(err)
	}
	loader, err := sessionhistory.New(sessionhistory.Options{Files: files, Stat: func(path string) (int64, error) {
		info, err := os.Stat(path)
		if err != nil {
			return 0, err
		}
		return info.Size(), nil
	}, ReadFile: files.ReadFile, DisablePrecompactSkip: func() bool { return false }, LeafPrune: func() bool { return true }, ParseDate: sessionprojection.ParseDateUTC, Compare: sessionhistory.CompareStrings, OnDiagnostic: func(event sessionhistory.Diagnostic) {
		if event.Message != "" {
			events.diagnostic(sessionresume.Diagnostic{Kind: "debug", Name: event.Message})
		}
		if event.Code != "" {
			events.diagnostic(sessionresume.Diagnostic{Kind: "event", Name: event.Code, Fields: event.Fields})
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	state, err := clisettings.NewState(func() float64 { return float64(events.millis()) })
	if err != nil {
		t.Fatal(err)
	}
	options := Options{ConfigDir: filepath.Join(home, ".claude"), CWD: home, OriginalCWD: home, CurrentSessionID: tempSID, NonInteractive: true, Environment: map[string]string{"CLAUDE_CODE_SIMPLE": "true"}, Files: files, Stat: func(path string) (Stat, error) {
		info, err := os.Stat(path)
		if err != nil {
			return Stat{}, err
		}
		return Stat{Modified: float64(info.ModTime().UnixMilli()), Birth: 0, Size: info.Size()}, nil
	}, Loader: loader, Reducer: reducer, ParseDate: sessionprojection.ParseDateUTC, Now: events.millis, UUID: uuid, OnDiagnostic: events.diagnostic, OnError: events.onError, OnPhase: func(phase string) { events.mu.Lock(); events.phases = append(events.phases, phase); events.mu.Unlock() }, PolicyPaths: []string{filepath.Join(home, "policy", "managed-settings.json")}, SettingsState: state}
	return options, events
}
func plain(uuid, content string) json.RawMessage {
	return encode(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("user")}, protocol.JSONField{Name: "uuid", Value: quote(uuid)}, protocol.JSONField{Name: "timestamp", Value: quote("2025-01-01T00:00:00Z")}, protocol.JSONField{Name: "sessionId", Value: quote(oldSID)}, protocol.JSONField{Name: "message", Value: encode(protocol.NewJSONObject(protocol.JSONField{Name: "role", Value: quote("user")}, protocol.JSONField{Name: "content", Value: quote(content)}))}))
}
func original(messages []json.RawMessage, fields ...protocol.JSONField) Input {
	fields = append([]protocol.JSONField{{Name: "messages", Value: encode(messages)}}, fields...)
	return Input{Source: encode(protocol.NewJSONObject(fields...))}
}
func TestSettingsProfileRejectsUnsupportedAndInvalidBeforeEffects(t *testing.T) {
	for _, raw := range []string{`{"model":3}`, `{"future":{"z":1}}`, `{"cleanupPeriodDays":"0"}`, `{"cleanupPeriodDays":-1}`, `{"cleanupPeriodDays":0.5}`, `{"cleanupPeriodDays":1e400}`, `{"cleanupPeriodDays":9007199254740992}`, `{"enabledPlugins":{"\ud800":false}}`, `{"disableAllHooks":null}`, `{"plansDirectory":3}`, `{"hooks":null}`, `{"hooks":{"SessionStart":{}}}`, `{"hooks":{"SessionStart":[null]}}`, `{"hooks":{"SessionStart":[{"hooks":null}]}}`, `{"hooks":{"SessionStart":[{"matcher":3,"hooks":[]}]}}`, `{"hooks":{"SessionStart":[{"future":true,"hooks":[]}]}}`, `{"hooks":{"SessionStart":[{"hooks":[{"type":"prompt","prompt":"p"}]}]}}`, `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":3}]}]}}`, `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"p","async":true}]}]}}`, `{"hooks":{"Stop":[]}}`, `{"enabledPlugins":3}`, `{"enabledPlugins":{"p@fixture":3}}`, `{"extraKnownMarketplaces":[]}`, `{"extraKnownMarketplaces":{"market":{}}}`, `{"\ud800":"protected"}`} {
		t.Run(raw, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, ".claude", "settings.json")
			writePrivate(t, path, raw)
			options, events := testOptions(t, home, OSFiles{})
			service, err := New(options)
			var unsupported *CapabilityError
			if service != nil || !errors.As(err, &unsupported) {
				t.Fatalf("invalid profile escaped: %v", err)
			}
			if len(events.phases) != 0 || len(events.allocations) != 0 {
				t.Fatalf("restoration ran on unsupported settings: %+v", events)
			}
			bytes, err := os.ReadFile(path)
			if err != nil || string(bytes) != raw {
				t.Fatal("protected settings mutated")
			}
			if _, err := os.Stat(filepath.Join(home, ".claude", "plans")); !errors.Is(err, fs.ErrNotExist) {
				t.Fatal("plan effects escaped validation")
			}
		})
	}
}
func TestCachedPolicyIsValidatedBeforeRestoration(t *testing.T) {
	for _, raw := range []string{`{"allowManagedHooksOnly":3}`, `{"hooks":null}`, `{"model":3}`, `{"\ud800":"protected"}`} {
		t.Run(raw, func(t *testing.T) {
			home := t.TempDir()
			options, events := testOptions(t, home, OSFiles{})
			merged := protocol.NewJSONObject()
			policy := object([]byte(raw))
			options.SettingsState.SetSessionSettingsCache(&clisettings.ParsedSettings{Settings: &merged})
			options.SettingsState.SetCachedSettingsForSource("policySettings", &policy)
			service, err := New(options)
			var unsupported *CapabilityError
			if service != nil || !errors.As(err, &unsupported) {
				t.Fatalf("unsupported cached policy escaped: %v", err)
			}
			if len(events.allocations) != 0 || len(events.phases) != 0 {
				t.Fatal("cached policy validation ran after effects")
			}
			if _, err := os.Stat(filepath.Join(home, ".claude", "plans")); !errors.Is(err, fs.ErrNotExist) {
				t.Fatal("unsupported cached policy made plan directory")
			}
		})
	}
}
func TestActiveHookPreflightPreservesPlanAndBackupSentinels(t *testing.T) {
	home := t.TempDir()
	options, events := testOptions(t, home, OSFiles{})
	options.Environment["CLAUDE_CODE_SIMPLE"] = "false"
	path := filepath.Join(home, ".claude", "settings.json")
	writePrivate(t, path, `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"must-never-execute"}]}]}}`)
	backup := filepath.Join(home, ".claude", "file-history", oldSID, "backup")
	writePrivate(t, backup, "protected-original")
	if service, err := New(options); service != nil || err == nil {
		t.Fatal("unimplemented active hook profile proceeded to restoration")
	}
	data, err := os.ReadFile(backup)
	if err != nil || string(data) != "protected-original" || len(events.allocations) != 0 || len(events.phases) != 0 {
		t.Fatal("capability rejection changed backup or generation order")
	}
}
func TestActiveHooksPluginsAndIncompleteHostAreExplicitCapabilities(t *testing.T) {
	for _, setup := range []struct {
		name, settings, policy, registry string
		args                             []string
		interactive                      bool
	}{{name: "active-hook", settings: `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"never-run"}]}]}}`}, {name: "enabled-plugin", settings: `{"enabledPlugins":{"p@fixture":true}}`}, {name: "registry", registry: `{"version":2,"plugins":{"p@fixture":[]}}`}, {name: "flag", args: []string{"--settings", "source.json"}}, {name: "interactive", interactive: true}} {
		t.Run(setup.name, func(t *testing.T) {
			home := t.TempDir()
			options, _ := testOptions(t, home, OSFiles{})
			options.Environment["CLAUDE_CODE_SIMPLE"] = "false"
			options.Arguments = setup.args
			options.NonInteractive = !setup.interactive
			if setup.settings != "" {
				writePrivate(t, filepath.Join(home, ".claude", "settings.json"), setup.settings)
			}
			if setup.registry != "" {
				writePrivate(t, filepath.Join(home, ".claude", "plugins", "installed_plugins.json"), setup.registry)
			}
			service, err := New(options)
			if err == nil {
				_, err = service.Load(original([]json.RawMessage{plain("u", "q")}))
				service.AwaitBackground()
			}
			var unsupported *CapabilityError
			if !errors.As(err, &unsupported) {
				t.Fatalf("active/unimplemented capability silently continued: %v", err)
			}
			if _, err := os.Stat(filepath.Join(home, ".claude", "projects")); !errors.Is(err, fs.ErrNotExist) {
				t.Fatal("unimplemented profile wrote a transcript")
			}
		})
	}
}
func TestSharedSettingsStateAndOneShotSkillSuppression(t *testing.T) {
	home := t.TempDir()
	options, _ := testOptions(t, home, OSFiles{})
	writePrivate(t, filepath.Join(home, ".claude", "settings.json"), `{"plansDirectory":"first"}`)
	service, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	writePrivate(t, filepath.Join(home, ".claude", "settings.json"), `{"plansDirectory":"second"}`)
	listing := json.RawMessage(`{"type":"attachment","attachment":{"type":"skill_listing","content":"listed"}}`)
	result, err := service.Load(original([]json.RawMessage{listing, plain("u", "q")}, protocol.JSONField{Name: "sessionId", Value: quote(oldSID)}))
	if err != nil || result == nil {
		t.Fatal(err)
	}
	if !service.State().SuppressNextSkillListing || !service.ConsumeSkillListingSuppression() || service.ConsumeSkillListingSuppression() {
		t.Fatal("suppression was lost or not one-shot")
	}
	settings, _, err := service.settings()
	if err != nil || text(settings.Get("plansDirectory")) != "first" {
		t.Fatal("real shared startup snapshot was re-read")
	}
	options.SettingsState.ResetSettingsCache()
	settings, _, err = service.settings()
	if err != nil || text(settings.Get("plansDirectory")) != "second" {
		t.Fatal("shared settings reset did not reload original file")
	}
}
func TestPrepareOwnsOnlyActualRestoredHistory(t *testing.T) {
	home := t.TempDir()
	options, _ := testOptions(t, home, OSFiles{})
	service, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Load(original([]json.RawMessage{plain("u", "01234567 original")}))
	if err != nil {
		t.Fatal(err)
	}
	ctx := requestcontext.Context{SystemPrompt: []json.RawMessage{}, Thinking: requestcontext.Thinking{Type: "disabled"}, Options: requestcontext.Options{Model: "fixture-model", QuerySource: "sdk", IsNonInteractiveSession: true}, Host: requestcontext.Host{Profile: requestcontext.DirectRelayProfile, Env: map[string]string{"ANTHROPIC_BASE_URL": "http://127.0.0.1:43210/proxy", "ANTHROPIC_API_KEY": "offline-fixture-key"}, DeviceID: "fixture-device", SessionID: oldSID, SDKBetas: []string{}, TTLAllowlist: []string{}}}
	deps := requestcontext.Dependencies{UUID: options.UUID, Now: func() time.Time { return time.UnixMilli(1700000000000) }}
	prepared, err := Prepare(result, ctx, deps)
	if err != nil {
		t.Fatal(err)
	}
	first, err := prepared.BuildAttempt(requestcontext.AttemptOptions{Model: "fixture-model"})
	if err != nil || !strings.Contains(string(first.PreparedRequest.Body), "01234567 original") {
		t.Fatalf("actual history missing: %v", err)
	}
	second, err := prepared.BuildAttempt(requestcontext.AttemptOptions{Model: "fixture-model"})
	if err != nil || string(first.PreparedRequest.Body) != string(second.PreparedRequest.Body) {
		t.Fatal("retry re-normalized or drifted history")
	}
	ctx.History = []protocol.JSONObject{}
	if _, err = Prepare(result, ctx, deps); err == nil {
		t.Fatal("accepted Source-prepared alternate history")
	}
	if _, err = Prepare(nil, requestcontext.Context{}, deps); err == nil {
		t.Fatal("accepted failed restoration")
	}
}
