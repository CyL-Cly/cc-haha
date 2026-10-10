package systemprompt

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func testOptions(t *testing.T) Options {
	t.Helper()
	environment, err := clistartupenv.NewEnvironment(protocol.NewJSONObject(protocol.JSONField{Name: "SHELL", Value: quoted("/bin/zsh")}))
	if err != nil {
		t.Fatal(err)
	}
	return Options{Environment: environment, CWD: func() string { return "/private/project" }, Model: "deepseek-v4-flash", Platform: "darwin", OSInfo: func() (OSInfo, error) { return OSInfo{Type: "Darwin", Release: "25.0.0"}, nil }, IsGit: func(context.Context) (bool, error) { return false, nil }, InitialSettings: func() (protocol.JSONObject, error) {
		return protocol.NewJSONObject(protocol.JSONField{Name: "autoMemoryEnabled", Value: json.RawMessage("false")}), nil
	}, SessionStartDate: func() json.RawMessage { return quoted("2026-10-05") }, NonInteractive: func() bool { return false }, SkillCommands: func(context.Context, string) ([]Command, error) { return nil, nil }, OutputStyle: func(context.Context, string) (*OutputStyle, error) { return nil, nil }}
}

func TestSectionCacheRetainsNullAndMCPVolatilityUntilClear(t *testing.T) {
	options := testOptions(t)
	var settingsCalls, environmentCalls atomic.Int32
	language := ""
	instructions := "first"
	options.InitialSettings = func() (protocol.JSONObject, error) {
		settingsCalls.Add(1)
		return protocol.NewJSONObject(protocol.JSONField{Name: "language", Value: quoted(language)}, protocol.JSONField{Name: "autoMemoryEnabled", Value: json.RawMessage("false")}), nil
	}
	options.OSInfo = func() (OSInfo, error) { environmentCalls.Add(1); return OSInfo{Type: "Darwin", Release: "25.0.0"}, nil }
	options.MCPClients = func() []MCPClient { return []MCPClient{{Name: "local", Type: "connected", Instructions: instructions}} }
	service, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.Resolve(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	language = "中文"
	instructions = "second"
	second, err := service.Resolve(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != len(second) {
		t.Fatalf("cached absent language must stay absent: %d -> %d", len(first), len(second))
	}
	if settingsCalls.Load() != 2 || environmentCalls.Load() != 3 {
		t.Fatalf("source initial producer and cached dynamic timing: settings=%d OS=%d", settingsCalls.Load(), environmentCalls.Load())
	}
	if rawString(second[len(second)-2]) != "# MCP Server Instructions\n\nThe following MCP servers have provided instructions for how to use their tools and resources:\n\n## local\nsecond" {
		t.Fatal("MCP instructions did not refresh")
	}
	service.Clear()
	third, err := service.Resolve(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(third) != len(second)+1 || environmentCalls.Load() != 5 {
		t.Fatal("clear must reset absent section and dynamic environment")
	}
}

func TestClearDoesNotCancelAlreadyRunningSectionProducer(t *testing.T) {
	service, err := New(testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		_, err := service.resolveSections([]section{{name: "shared", compute: func() (*string, error) { close(started); <-release; return pointer("old producer"), nil }}})
		finished <- err
	}()
	<-started
	service.Clear()
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	called := false
	values, err := service.resolveSections([]section{{name: "shared", compute: func() (*string, error) { called = true; return pointer("new"), nil }}})
	if err != nil || called || values[0] == nil || *values[0] != "old producer" {
		t.Fatalf("source in-flight producer re-populates current cache: %v %v %v", values, called, err)
	}
}

func TestSimplePreservesJavaScriptDateCodeUnitsAndSkipsProducers(t *testing.T) {
	options := testOptions(t)
	if err := options.Environment.SetProperty(quoted("CLAUDE_CODE_SIMPLE"), quoted("1")); err != nil {
		t.Fatal(err)
	}
	options.SessionStartDate = func() json.RawMessage { return json.RawMessage(`"\ud800"`) }
	options.InitialSettings = func() (protocol.JSONObject, error) {
		t.Error("simple mode read settings")
		return protocol.NewJSONObject(), nil
	}
	options.SkillCommands = func(context.Context, string) ([]Command, error) {
		t.Error("simple mode loaded skills")
		return nil, nil
	}
	service, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	sections, err := service.Resolve(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	units, err := protocol.DecodeJavaScriptJSONString(sections[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(sections) != 1 || units[len(units)-1] != 0xd800 {
		t.Fatalf("simple date lost UTF16: %s", sections)
	}
}
