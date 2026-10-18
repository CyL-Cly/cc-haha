package clibootstrap

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestTerminalConfigTeamsModeSavesClearsCLIOverrideAndEscapeRetainsRuntimeSnapshot(t *testing.T) {
	m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", func(n *NativeBoundary) { n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1" }, func(options *MainPrintOptions) { options.Interactive = true; options.Arguments.TeammateMode = "tmux" })
	snapshot, err := m.TerminalConfigState(context.Background())
	if err != nil || string(snapshot.Get("teamsEnabled")) != "true" || snapshot.String("teammateModeOverride") != "tmux" {
		t.Fatal("actual enabled Config teams rows have no native producer", snapshot, err)
	}
	if err = m.SetTerminalConfigSetting(context.Background(), "teammateMode", quoteMain("in-process")); err != nil {
		t.Fatal(err)
	}
	if m.options.Arguments.TeammateMode != "" || m.teamLeader.mode != "in-process" {
		t.Fatal("UI did not clear actual CLI override/current snapshot", m.options.Arguments.TeammateMode, m.teamLeader.mode)
	}
	if err = m.RestoreTerminalConfig(context.Background(), snapshot, []string{"teammateMode"}); err != nil {
		t.Fatal(err)
	}
	global, err := m.Global.Get()
	if err != nil {
		t.Fatal(err)
	}
	if len(global.Get("teammateMode")) != 0 || m.teamLeader.mode != "in-process" || m.options.Arguments.TeammateMode != "" {
		t.Fatal("Escape restored wrong store/snapshot", global, m.teamLeader.mode, m.options.Arguments.TeammateMode)
	}
}

func TestTerminalTeammateModelPickerDefaultNoopIgnoredEffortAndActualNextSpawnModel(t *testing.T) {
	var mu sync.Mutex
	var requests []protocol.JSONObject
	artifact := ""
	presetArtifact := ""
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, nativeInProcessObject(body))
		index := len(requests)
		mu.Unlock()
		if index == 1 {
			emitNativeTaskTool(w, "team-config-actual-write", "Write", map[string]any{"file_path": artifact, "content": "TEAM_CONFIG_HAIKU_ACTUAL_ARTIFACT"})
		} else if index == 3 {
			emitNativeTaskTool(w, "team-config-preset-write", "Write", map[string]any{"file_path": presetArtifact, "content": "TEAM_CONFIG_PRESET_ACTUAL_ARTIFACT"})
		} else {
			emitPlanFixture(w, index, "", "TEAM_CONFIG_ACTUAL_CHILD_DONE")
		}
	}))
	defer peer.Close()
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
		n.Environment["ANTHROPIC_DEFAULT_HAIKU_MODEL"] = "owned-team-haiku"
	}, func(options *MainPrintOptions) {
		options.Interactive = true
		options.Arguments.Tools = []string{"TeamCreate,TeamDelete,Agent,Read,Write,SendMessage"}
	})
	artifact = filepath.Join(m.workingDirectory(), "team-config-haiku.txt")
	presetArtifact = filepath.Join(m.workingDirectory(), "team-config-preset.txt")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var before sessioncontroller.NativeTypedModelState
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		var err error
		before, err = m.Controller.TypedModelStateOnOwner(scope)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	userPath := filepath.Join(m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "settings.json")
	userBefore, _ := os.ReadFile(userPath)
	globalBefore, err := m.Global.Get()
	if err != nil {
		t.Fatal(err)
	}
	picker, err := m.NativeTeammateModelPicker(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if picker.Initial != nil || !picker.SkipSettingsWrite || !strings.HasPrefix(picker.HeaderText, "Default model for newly spawned teammates.") {
		t.Fatal(picker)
	}
	changed, err := m.ApplyNativeTeammateModelPicker(ctx, picker.ID, tui.ModelPickerResult{})
	if err != nil || changed {
		t.Fatal("unset default confirm must be no-op", changed, err)
	}
	globalAfter, err := m.Global.Get()
	if err != nil || !reflect.DeepEqual(globalBefore, globalAfter) {
		t.Fatal("default no-op mutated global", globalBefore, globalAfter, err)
	}
	picker, err = m.NativeTeammateModelPicker(ctx)
	if err != nil {
		t.Fatal(err)
	}
	selected := "haiku"
	effort := "max"
	changed, err = m.ApplyNativeTeammateModelPicker(ctx, picker.ID, tui.ModelPickerResult{Model: &selected, Effort: &effort, HasToggledEffort: true})
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	globalAfter, err = m.Global.Get()
	if err != nil || globalAfter.String("teammateDefaultModel") != "haiku" {
		t.Fatal(globalAfter, err)
	}
	userAfter, _ := os.ReadFile(userPath)
	var after sessioncontroller.NativeTypedModelState
	if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		var err error
		after, err = m.Controller.TypedModelStateOnOwner(scope)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) || string(userBefore) != string(userAfter) {
		t.Fatal("skipSettingsWrite callback changed leader model/effort/settings", before, after, string(userAfter))
	}
	callTerminalTeamFooterActualTool(t, ctx, m, "TeamCreate", `{"team_name":"config-model"}`)
	callTerminalTeamFooterActualTool(t, ctx, m, "Agent", `{"description":"Real configured plain teammate","name":"worker","prompt":"Write the actual configured child artifact"}`)
	for {
		data, err := os.ReadFile(artifact)
		mu.Lock()
		count := len(requests)
		mu.Unlock()
		if err == nil && string(data) == "TEAM_CONFIG_HAIKU_ACTUAL_ARTIFACT" && count >= 2 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("saved model did not drive real child Harness", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	// The original resolver gives a selected Agent definition precedence over
	// the plain-teammate default; omitted/inherit frontmatter follows leader.
	callTerminalTeamFooterActualTool(t, ctx, m, "Agent", `{"description":"Real selected Agent teammate","name":"preset-worker","prompt":"Write the actual selected Agent artifact","subagent_type":"general-purpose"}`)
	for {
		data, err := os.ReadFile(presetArtifact)
		mu.Lock()
		count := len(requests)
		mu.Unlock()
		if err == nil && string(data) == "TEAM_CONFIG_PRESET_ACTUAL_ARTIFACT" && count >= 4 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("selected Agent branch did not drive real child Harness", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for i, request := range requests {
		want := "owned-team-haiku"
		if i >= 2 {
			want = "fixture-model"
		}
		if request.String("model") != want {
			t.Fatal("wrong actual next-spawn model", i, request.String("model"))
		}
		if !json.Valid(rawMain(request)) {
			t.Fatal("invalid actual child body")
		}
	}
}
