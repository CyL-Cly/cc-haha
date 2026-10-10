package usercontext

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

type fixtureStep struct {
	Kind, Source, Raw string
	Value             json.RawMessage
	Delete            bool
	Sources           []*protocol.JSONObject
}
type fixtureInput struct {
	Home, Name, Timezone, Memory, CustomSystemPrompt string
	Steps                                            []fixtureStep
}

func mustFixture(err error) {
	if err != nil {
		panic(err)
	}
}
func encodedFixture(value any) string {
	raw, err := json.Marshal(value)
	mustFixture(err)
	raw, err = protocol.ParseAndStringifyJavaScriptJSON(raw)
	mustFixture(err)
	return string(raw)
}

type memoryBarrier struct {
	OSMemoryFiles
	clock            func() time.Time
	settled, release chan struct{}
	mu               sync.Mutex
	rows             []any
}

func (f *memoryBarrier) ReadFile(path string) ([]byte, error) {
	f.mu.Lock()
	f.rows = append(f.rows, map[string]any{"kind": "read-start", "millis": f.clock().UnixMilli(), "path": path})
	f.mu.Unlock()
	content, err := f.OSMemoryFiles.ReadFile(path)
	f.mu.Lock()
	f.rows = append(f.rows, map[string]any{"kind": "native-read-settled", "millis": f.clock().UnixMilli(), "content": string(content)})
	f.mu.Unlock()
	close(f.settled)
	<-f.release
	f.mu.Lock()
	f.rows = append(f.rows, map[string]any{"kind": "read-release", "millis": f.clock().UnixMilli()})
	f.mu.Unlock()
	return content, err
}
func (f *memoryBarrier) snapshot() []any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]any{}, f.rows...)
}
func runFixture(input fixtureInput) any {
	home := input.Home
	config := filepath.Join(home, ".claude")
	project := filepath.Join(home, "project")
	userPath := filepath.Join(config, "settings.json")
	mustFixture(os.MkdirAll(filepath.Join(project, ".claude"), 0700))
	mustFixture(os.MkdirAll(config, 0700))
	initial := "{}"
	if input.Memory != "" {
		initial = `{"pluginConfigs":{"agents-md@builtin":{"options":{"instructionFiles":"claude-md"}}}}`
		mustFixture(os.WriteFile(filepath.Join(config, "CLAUDE.md"), []byte("Private original probe instructions.\n"), 0600))
	}
	mustFixture(os.WriteFile(userPath, []byte(initial), 0600))
	location, err := time.LoadLocation(input.Timezone)
	mustFixture(err)
	var clockMu sync.Mutex
	clock, _ := time.Parse(time.RFC3339, "2026-10-01T23:59:59.000Z")
	now := func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return clock.In(location) }
	setClock := func(value string) {
		next, err := time.Parse(time.RFC3339, value)
		mustFixture(err)
		clockMu.Lock()
		clock = next
		clockMu.Unlock()
	}
	state, err := clisettings.NewState(func() float64 { return float64(now().UnixMilli()) })
	mustFixture(err)
	settings, err := clisettings.NewService(clisettings.Options{State: state, ConfigHome: config, OriginalCWD: project, Profile: clisettings.ExternalReleaseProfile, Now: func() float64 { return float64(now().UnixMilli()) }, PID: 12345})
	mustFixture(err)
	env, err := clistartupenv.NewEnvironment(protocol.NewJSONObject(protocol.JSONField{Name: "CLAUDE_CODE_SIMPLE", Value: quote("1")}, protocol.JSONField{Name: "CLAUDE_CODE_DISABLE_CLAUDE_MDS", Value: quote("1")}))
	mustFixture(err)
	options := Options{Environment: env, Settings: settings, SettingsState: state, Now: now, Profile: ExternalProfile}
	var barrier *memoryBarrier
	if input.Memory != "" {
		mustFixture(env.SetProperty(quote("CLAUDE_CODE_DISABLE_CLAUDE_MDS"), quote("0")))
		barrier = &memoryBarrier{clock: now, settled: make(chan struct{}), release: make(chan struct{})}
		options.AdditionalDirectories = []string{project}
		options.Memory = &ExplicitUserMemory{ConfigHome: config, Profile: ExplicitUserMemoryProfile, Files: barrier}
	}
	service, err := New(options)
	mustFixture(err)
	allocations := []any{}
	serial := 0
	uuid := func() string {
		serial++
		value := fmt.Sprintf("00000000-0000-4000-8000-%012d", serial)
		allocations = append(allocations, map[string]any{"kind": "uuid", "value": value, "millis": now().UnixMilli()})
		return value
	}
	factory, err := messages.NewFactory(uuid, now)
	mustFixture(err)
	results := []any{}
	var previousPromise *Future
	var previousValue *protocol.JSONObject
	for _, step := range input.Steps {
		start := len(allocations)
		var result any
		switch step.Kind {
		case "clock":
			setClock(rawString(step.Value))
		case "override":
			if step.Delete {
				snapshot := env.Snapshot()
				mustFixture(snapshot.DeletePropertyJSON(quote("CLAUDE_CODE_OVERRIDE_DATE")))
				env, err = clistartupenv.NewEnvironment(snapshot)
				mustFixture(err)
				service.options.Environment = env
			} else {
				value := step.Value
				if step.Raw != "" {
					value = []byte(step.Raw)
				}
				mustFixture(env.SetProperty(quote("CLAUDE_CODE_OVERRIDE_DATE"), value))
			}
		case "flagSettings":
			if string(step.Value) == "null" {
				service.SetFlagSettings(nil)
			} else {
				flag := object(step.Value)
				service.SetFlagSettings(&flag)
			}
		case "mutateUser":
			if previousValue == nil {
				panic("mutation requires actual existing memo")
			}
			previousValue.Set("currentDate", step.Value)
		case "writeMode":
			path := userPath
			if step.Source == "project" {
				path = filepath.Join(project, ".claude/settings.json")
			}
			mustFixture(os.WriteFile(path, []byte(`{"pluginConfigs":{"agents-md@builtin":{"options":{"instructionFiles":`+string(step.Value)+`}}}}`), 0600))
		case "resetSettings":
			state.ResetSettingsCache()
		case "clearUser":
			service.ClearUserContext()
		case "clearSessionDate":
			service.ClearSessionStartDate()
		case "injection":
			mustFixture(service.SetSystemPromptInjection(step.Value))
		case "schema":
			mustFixture(os.WriteFile(userPath, []byte(step.Raw), 0600))
			state.ResetSettingsCache()
			parsed, err := settings.ParseFile(userPath)
			mustFixture(err)
			result = map[string]any{"settingsJSON": encodedFixture(parsed.Settings), "errorsJSON": encodedFixture(parsed.Errors)}
		case "modeSources":
			result = map[string]any{"mode": InstructionFilesModeFromSettings(step.Sources...)}
		case "read":
			session := service.GetSessionStartDate()
			future := service.GetUserContext()
			var pending any
			if barrier != nil && previousPromise == nil {
				<-barrier.settled
				pending = map[string]any{"samePromise": service.GetUserContext() == future, "cachedMemoryJSON": string(service.CachedClaudeMd())}
				setClock("2026-10-02T00:00:01.000Z")
				close(barrier.release)
			}
			parts := service.GetUserContext()
			value, err := future.Await(context.Background())
			mustFixture(err)
			partsValue, err := parts.Await(context.Background())
			mustFixture(err)
			content, err := RenderPrefix(*partsValue)
			mustFixture(err)
			envelope := messages.OrderedUser(factory.CreateUser(storage.Object{"content": content, "isMeta": json.RawMessage("true")}))
			mode, err := service.InstructionFilesMode()
			mustFixture(err)
			ioRows := []any{}
			if barrier != nil {
				ioRows = barrier.snapshot()
			}
			result = map[string]any{"sessionDateJSON": string(protocol.StringifyValidatedJavaScriptJSON(session)), "localDateJSON": string(protocol.StringifyValidatedJavaScriptJSON(service.GetLocalISODate())), "userContextJSON": encodedFixture(value), "prefixJSON": encodedFixture([]protocol.JSONObject{envelope}), "samePromise": previousPromise == future, "sameValue": previousValue == value, "partsUsesSameValue": partsValue == value, "cachedMemoryJSON": string(service.CachedClaudeMd()), "mode": mode, "cacheSize": 1, "pending": pending, "io": ioRows}
			previousPromise = future
			previousValue = value
		default:
			panic("undeclared original operation")
		}
		raw, _ := json.Marshal(step)
		var original protocol.JSONObject
		_ = original.UnmarshalJSON(raw)
		// fixtureStep exported fields are not the original input spelling.
		projected := protocol.NewJSONObject(protocol.JSONField{Name: "kind", Value: quote(step.Kind)})
		if step.Value != nil {
			projected.Set("value", step.Value)
		}
		if step.Source != "" {
			projected.Set("source", quote(step.Source))
		}
		if step.Delete {
			projected.Set("delete", []byte("true"))
		}
		if step.Raw != "" {
			projected.Set("raw", quote(step.Raw))
		}
		if step.Sources != nil {
			projected.Set("sources", []byte(encodedFixture(step.Sources)))
		}
		results = append(results, map[string]any{"step": projected, "result": result, "allocations": append([]any{}, allocations[start:]...)})
	}
	return map[string]any{"name": input.Name, "results": results}
}
func TestUserContextFixture(t *testing.T) {
	if os.Getenv("CC_HAHA_USER_CONTEXT_FIXTURE") != "1" {
		t.Skip("private fixture only")
	}
	var input fixtureInput
	decoder := json.NewDecoder(os.Stdin)
	decoder.DisallowUnknownFields()
	mustFixture(decoder.Decode(&input))
	if decoder.Decode(new(any)) != io.EOF {
		panic("one original input required")
	}
	home := input.Home
	info, err := os.Lstat(home)
	mustFixture(err)
	real, err := filepath.EvalSymlinks(home)
	mustFixture(err)
	cwd, err := os.Getwd()
	mustFixture(err)
	allocation := os.Getenv("CC_HAHA_FIXTURE_TEMP_ROOT")
	allocationReal, err := filepath.EvalSymlinks(allocation)
	mustFixture(err)
	relative, err := filepath.Rel(allocationReal, home)
	mustFixture(err)
	if !filepath.IsAbs(home) || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || real != home || cwd != home || allocation == "" || relative == "." || strings.HasPrefix(relative, "..") || os.Getenv("HOME") != home || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(home, ".claude") || os.Getenv("TZ") != input.Timezone || os.Getenv("NODE_ENV") != "production" {
		panic("private original HOME/config input required")
	}
	for _, path := range []string{filepath.Join(home, ".claude"), filepath.Join(home, ".claude", "settings.json"), filepath.Join(home, ".claude", "CLAUDE.md"), filepath.Join(home, "project"), filepath.Join(home, "project", ".claude"), filepath.Join(home, "project", ".claude", "settings.json")} {
		for current := path; current != home; current = filepath.Dir(current) {
			if info, err := os.Lstat(current); err == nil && info.Mode()&os.ModeSymlink != 0 {
				panic("private context path cannot use symlink")
			} else if err != nil && !os.IsNotExist(err) {
				panic(err)
			}
		}
	}
	result := runFixture(input)
	fmt.Print(encodedFixture(result))
}
