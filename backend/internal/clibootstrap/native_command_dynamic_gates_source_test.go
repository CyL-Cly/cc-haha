package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
)

func TestActualWarmCommandCatalogRechecksDynamicGatesWithoutReloadingMemo(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual pinned Source Bun required")
	}
	for _, initiallyEnabled := range []bool{false, true} {
		name := "initially-disabled"
		if initiallyEnabled {
			name = "initially-enabled"
		}
		t.Run(name, func(t *testing.T) { testActualWarmCommandDynamicGates(t, bun, initiallyEnabled) })
	}
}

type commandGateSelection struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	LoadedFrom  string `json:"loadedFrom"`
}
type commandGateFrame struct {
	Selected         []commandGateSelection `json:"selected"`
	SlashNames       []string               `json:"slashNames"`
	SlashSame        bool                   `json:"slashSame"`
	WorkflowIdentity bool                   `json:"workflowIdentity"`
}

func testActualWarmCommandDynamicGates(t *testing.T, bun string, initiallyEnabled bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	f := newNativePluginFixture(t)
	// Match the same disabled sample plugin on both actual getCommands arms.
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false},"future":{"keep":true}}`)
	delete(f.n.Environment, "CLAUDE_CODE_SIMPLE")
	f.n.Environment["ANTHROPIC_API_KEY"] = "offline-command-gates"
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-command-dynamic-gates.fixture.ts")
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = f.home
	command.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_COMMAND_DYNAMIC_GATES=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "ANTHROPIC_API_KEY=offline-command-gates", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "PATH=" + filepath.Join(f.home, "bin")}
	input, _ := json.Marshal(map[string]any{"home": f.home, "initialEnabled": initiallyEnabled})
	command.Stdin = bytes.NewReader(input)
	var output, diagnostic bytes.Buffer
	command.Stdout, command.Stderr = &output, &diagnostic
	if err := command.Run(); err != nil {
		t.Fatalf("actual full getCommands gates %v %s %s", err, output.String(), diagnostic.String())
	}
	var source struct {
		Frames []commandGateFrame
		Caps   struct{ Blocked, SecureReads []string }
	}
	if err := json.Unmarshal(output.Bytes(), &source); err != nil || len(source.Frames) != 4 || len(source.Caps.Blocked) != 0 || len(source.Caps.SecureReads) != 0 {
		t.Fatalf("actual Source gates: %v %s %s", err, output.String(), diagnostic.String())
	}
	for _, frame := range source.Frames {
		if !frame.SlashSame || !frame.WorkflowIdentity {
			t.Fatal("Source base/slash memo changed", output.String())
		}
	}
	has := func(frame commandGateFrame, name string) bool {
		for _, value := range frame.Selected {
			if value.Name == name {
				return true
			}
		}
		return false
	}
	if has(source.Frames[0], "computer-use") != initiallyEnabled || has(source.Frames[0], "imagegen") != initiallyEnabled || !has(source.Frames[0], "live-workflow") || has(source.Frames[1], "computer-use") == initiallyEnabled || has(source.Frames[1], "imagegen") == initiallyEnabled || has(source.Frames[1], "live-workflow") || len(source.Frames[2].Selected) != 2 || len(source.Frames[3].Selected) != 3 {
		t.Fatal("actual Source did not reach all gate transition arms", output.String())
	}
	if source.Frames[2].Selected[0].Name != "computer-use" || source.Frames[2].Selected[0].Description != "Dynamic computer replacement" || source.Frames[3].Selected[0].LoadedFrom != "bundled" {
		t.Fatal("Source disabled base must release the dynamic name, then win again when enabled", output.String())
	}
	workflowPath := filepath.Join(f.cwd, ".claude", "workflows", "live-workflow.js")
	workflowBody := func(description string) string {
		raw, _ := json.Marshal(description)
		return "export const meta={name:'live-workflow',description:" + string(raw) + "};return args"
	}
	f.write(t, workflowPath, workflowBody("Original cached workflow"))
	cuPath := filepath.Join(f.config, "cc-haha", "computer-use-config.json")
	imageEnvironment := func(enabled bool) map[string]string {
		values := map[string]string{"CC_HAHA_IMAGE_PROVIDER_KIND": "", "CC_HAHA_IMAGE_PROVIDER_ID": "", "CC_HAHA_IMAGE_MODEL": "", "CC_HAHA_IMAGE_BASE_URL": "", "CC_HAHA_IMAGE_API_KEY": ""}
		if enabled {
			values["CC_HAHA_IMAGE_PROVIDER_KIND"] = "openai_images"
			values["CC_HAHA_IMAGE_PROVIDER_ID"] = "owned"
			values["CC_HAHA_IMAGE_MODEL"] = "owned-model"
			values["CC_HAHA_IMAGE_BASE_URL"] = "http://127.0.0.1:1"
			values["CC_HAHA_IMAGE_API_KEY"] = "fixture-key"
		}
		return values
	}
	f.write(t, cuPath, string(rawMain(map[string]bool{"enabled": initiallyEnabled})))
	f.write(t, filepath.Join(f.config, "settings.json"), string(rawMain(map[string]any{"enabledPlugins": map[string]bool{"sample@mkt": false}, "enableWorkflows": true, "env": imageEnvironment(initiallyEnabled)})))
	disabled := false
	m, err := NewMainPrint(MainPrintOptions{Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "private command gates", Tools: []string{""}, Sources: DefaultState(), MaxTurns: 2, PermissionMode: "default"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer closeCancel()
		if err := m.ShutdownSession(closeCtx); err != nil {
			t.Error("actual command runtime close", err)
		}
	})
	base := m.commandCatalogLoad()
	var slash any
	var frames []commandGateFrame
	capture := func() {
		t.Helper()
		if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			definitions, _, err := m.nativeCommandDefinitions(ctx, scope)
			if err != nil {
				return err
			}
			frame := commandGateFrame{Selected: []commandGateSelection{}, SlashNames: []string{}, WorkflowIdentity: m.commandCatalogLoad() == base}
			for _, definition := range definitions {
				if definition.Name() != "computer-use" && definition.Name() != "imagegen" && definition.Name() != "live-workflow" {
					continue
				}
				metadata := definition.Metadata()
				if definition.Name() == "live-workflow" && metadata.String("description") != "Original cached workflow" {
					frame.WorkflowIdentity = false
				}
				frame.Selected = append(frame.Selected, commandGateSelection{definition.Name(), metadata.String("description"), metadata.String("loadedFrom")})
			}
			sort.Slice(frame.Selected, func(i, j int) bool { return frame.Selected[i].Name < frame.Selected[j].Name })
			current, err := m.beginNativeSlashCommandSkills(scope)
			if err != nil {
				return err
			}
			if slash == nil {
				slash = current
			}
			frame.SlashSame = slash == current
			names, err := current.Await(scope)
			if err != nil {
				return err
			}
			for _, raw := range names {
				var name string
				if err := json.Unmarshal(raw, &name); err != nil {
					return err
				}
				if name == "computer-use" || name == "imagegen" {
					frame.SlashNames = append(frame.SlashNames, name)
				}
			}
			sort.Strings(frame.SlashNames)
			frames = append(frames, frame)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if m.commandCatalogLoad() != base {
			t.Fatal("dynamic projection reloaded original base memo")
		}
	}
	setGates := func(enabled, workflow bool) {
		t.Helper()
		f.write(t, cuPath, string(rawMain(map[string]bool{"enabled": enabled})))
		if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			m.invalidateComputerUseSkillGate()
			result, err := m.settings.UpdateEditableSource("userSettings", "", clisettings.Patch{Object: mainObj(rawMain(map[string]any{"enableWorkflows": workflow, "env": imageEnvironment(enabled)}))})
			if err != nil {
				return err
			}
			if result.Error != nil {
				return result.Error
			}
			if err = m.Startup.ApplyConfigEnvironmentVariables(); err != nil {
				return err
			}
			// The actual Source fixture calls getCommands without mounting a
			// Session/Host or its watchers. Keep this projection at that layer.
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	capture()
	f.write(t, workflowPath, workflowBody("Changed disk workflow must stay memoized"))
	setGates(!initiallyEnabled, false)
	capture()
	setGates(false, true)
	dynamicDir := filepath.Join(f.home, "dynamic-skills")
	f.write(t, filepath.Join(dynamicDir, "computer-use", "SKILL.md"), "---\ndescription: Dynamic computer replacement\n---\nActual dynamically loaded skill.")
	if err = m.skillCommands.AddDirectories(ctx, []string{dynamicDir}); err != nil {
		t.Fatal(err)
	}
	capture()
	setGates(true, true)
	capture()
	if !reflect.DeepEqual(frames, source.Frames) {
		t.Fatalf("warmed command projection differs\nGo=%s\nSource=%s", rawMain(frames), rawMain(source.Frames))
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if err = foreign.Run(ctx, func(scope *cooperative.Scope) error {
		_, _, err := m.nativeCommandDefinitions(ctx, scope)
		if !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("foreign gate projection", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// Keep the old filtered API contract distinct from the memoized raw catalog.
func TestRegisteredBundledCatalogKeepsImagegenPredicateAndFilteredAPI(t *testing.T) {
	environment := map[string]string{}
	base, err := skillcommands.NativeExternalBundledCatalog(skillcommands.BundledOptions{Environment: environment})
	if err != nil {
		t.Fatal(err)
	}
	var image *skillcommands.Definition
	for i := range base {
		if base[i].Name() == "imagegen" {
			image = &base[i]
		}
	}
	if image == nil {
		t.Fatal("registered catalog discarded disabled imagegen definition")
	}
	for _, enabled := range []bool{false, true, false} {
		if enabled {
			environment["CC_HAHA_IMAGE_PROVIDER_KIND"] = "grok_oauth"
			environment["CC_HAHA_IMAGE_PROVIDER_ID"] = "owned"
			environment["CC_HAHA_IMAGE_MODEL"] = "owned"
		} else {
			environment["CC_HAHA_IMAGE_PROVIDER_KIND"] = ""
		}
		got, err := image.IsEnabled(nil)
		if err != nil || got != enabled {
			t.Fatal("registered gate froze", got, enabled, err)
		}
		filtered, err := skillcommands.NativeExternalBundled(skillcommands.BundledOptions{Environment: environment})
		if err != nil {
			t.Fatal(err)
		}
		present := false
		for _, definition := range filtered {
			present = present || definition.Name() == "imagegen"
		}
		if present != enabled {
			t.Fatal("filtered legacy API changed")
		}
	}
}
