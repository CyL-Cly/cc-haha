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
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
)

func TestActualQueryInitCatalogKeepsWarmMemoAndInvalidatesOnlyOriginalSources(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual pinned Source Bun required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	f := newNativePluginFixture(t)
	// usercontext.ExternalProfile requires no active plugin settings base.
	// Keep the same real, disabled plugin fixture on both Source/native arms;
	// full/cache loader identities remain actual operations rather than mocks.
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false},"future":{"keep":true}}`)
	f.n.Environment["ANTHROPIC_API_KEY"] = "offline-query-catalog"
	skill := filepath.Join(f.cwd, ".claude", "skills", "held", "SKILL.md")
	payload := "---\ndescription: Before cache edit\n---\nActual catalog memo body."
	f.write(t, skill, payload)
	secondCwd := filepath.Join(f.home, "other-project")
	f.write(t, filepath.Join(secondCwd, ".claude", "skills", "second", "SKILL.md"), "---\ndescription: Second actual cwd skill\n---\nSecond cwd body.")
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-query-init-catalog.fixture.ts")
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = f.home
	command.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_QUERY_INIT_CATALOG_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "ANTHROPIC_API_KEY=offline-query-catalog", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "PATH=" + filepath.Join(f.home, "bin")}
	input, _ := json.Marshal(map[string]any{"home": f.home, "peer": "http://127.0.0.1:1", "skill": skill, "cacheLifecycle": true, "secondCwd": secondCwd})
	command.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("actual Source cache lifecycle %v %s %s", err, stdout.String(), stderr.String())
	}
	var source struct {
		WarmSkillsSame, ClearedSkillsDifferent, FullWarmDistinct, FullWarmSame, SlashAfterFullSame, PluginClearDifferent, OtherCwdDistinct, FirstCwdPromiseSame bool
		WarmDescription, ClearDescription                                                                                                                       string
		OtherNames, Blocked                                                                                                                                     []string
	}
	if err := json.Unmarshal(stdout.Bytes(), &source); err != nil || len(source.Blocked) > 0 {
		t.Fatalf("actual Source caches %v %s", err, stdout.String())
	}
	if !source.WarmSkillsSame || !source.ClearedSkillsDifferent || !source.FullWarmDistinct || !source.FullWarmSame || !source.SlashAfterFullSame || !source.PluginClearDifferent || !source.OtherCwdDistinct || !source.FirstCwdPromiseSame || !strings.Contains(string(rawMain(source.OtherNames)), `"second"`) || strings.Contains(string(rawMain(source.OtherNames)), `"held"`) || source.WarmDescription != "Before cache edit" || source.ClearDescription != "After cache edit" {
		t.Fatalf("original actual cache contract %s", stdout.String())
	}
	f.write(t, skill, payload)
	disabled := false
	m, err := NewMainPrint(MainPrintOptions{Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "private actual init cache", Tools: []string{""}, Sources: DefaultState(), MaxTurns: 2, PermissionMode: "default"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer closeCancel()
		_ = m.ShutdownSession(closeCtx)
	})
	if err = m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	queryCatalogJoinStartupInstallation(t, ctx, m)
	var beforeSkills, afterSkills *ownerasync.OwnerFuture[[]json.RawMessage]
	var beforePlugins, afterPlugins *ownerasync.OwnerFuture[pluginregistry.Result]
	if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		var err error
		beforeSkills, err = m.beginNativeSlashCommandSkills(scope)
		if err != nil {
			return err
		}
		if _, err = beforeSkills.Await(scope); err != nil {
			return err
		}
		beforePlugins, err = m.beginNativePluginCatalog(scope)
		if err != nil {
			return err
		}
		if _, err = beforePlugins.Await(scope); err != nil {
			return err
		}
		warm, err := m.beginNativeSlashCommandSkills(scope)
		if err != nil {
			return err
		}
		if warm != beforeSkills {
			t.Error("warm actual skills lost Promise identity")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.write(t, skill, "---\ndescription: After cache edit\n---\nChanged real skill body.")
	definitions, _, err := m.nativeCommandDefinitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if queryCatalogDescription(definitions, "held") != "Before cache edit" {
		t.Fatal("disk edit bypassed original command memo")
	}
	m.skillCommands.Clear()
	if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		var err error
		afterSkills, err = m.beginNativeSlashCommandSkills(scope)
		if err != nil {
			return err
		}
		if afterSkills == beforeSkills {
			t.Error("actual command clear retained old slash Promise")
		}
		_, err = afterSkills.Await(scope)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	definitions, _, err = m.nativeCommandDefinitions(ctx)
	if err != nil || queryCatalogDescription(definitions, "held") != "After cache edit" {
		t.Fatal("clear did not read actual changed skill", err)
	}
	if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		if err := m.nativeBashCwdChanged(scope, f.cwd, secondCwd); err != nil {
			return err
		}
		other, err := m.beginNativeSlashCommandSkills(scope)
		if err != nil {
			return err
		}
		if other == afterSkills {
			t.Error("different actual cwd reused first command/slash Promise")
		}
		names, err := other.Await(scope)
		if err != nil {
			return err
		}
		if !bytes.Contains(rawMain(names), []byte(`"second"`)) || bytes.Contains(rawMain(names), []byte(`"held"`)) {
			t.Errorf("actual changed cwd skills %s", rawMain(names))
		}
		if err = m.nativeBashCwdChanged(scope, secondCwd, f.cwd); err != nil {
			return err
		}
		back, err := m.beginNativeSlashCommandSkills(scope)
		if err != nil {
			return err
		}
		if back != afterSkills {
			t.Error("returning to cwd lost original memo identity")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	loader := m.nativeQueryCatalog.loader
	cachedBefore := loader.CachedProducer()
	full := loader.FullProducer()
	if _, err = m.nativeSettings.LoadPlugins(ctx, "", true); err != nil {
		t.Fatal("actual full plugin refresh", err)
	}
	cachedWarm := loader.CachedProducer()
	if cachedWarm == cachedBefore || cachedWarm == full || loader.FullProducer() != full {
		t.Fatal("full refresh did not install original distinct completed cached-only operation")
	}
	if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		var err error
		afterPlugins, err = m.beginNativePluginCatalog(scope)
		if err != nil {
			return err
		}
		if afterPlugins == beforePlugins {
			t.Error("full warm retained obsolete Owner plugin Promise")
		}
		if _, err = afterPlugins.Await(scope); err != nil {
			return err
		}
		slash, err := m.beginNativeSlashCommandSkills(scope)
		if err != nil {
			return err
		}
		if slash != afterSkills {
			t.Error("full plugin refresh implicitly cleared command memo")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	loader.Clear()
	if loader.CachedProducer() == cachedWarm {
		t.Fatal("explicit plugin clear retained old native operation")
	}
	if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		cleared, err := m.beginNativePluginCatalog(scope)
		if err != nil {
			return err
		}
		if cleared == afterPlugins {
			t.Error("plugin clear retained Owner Promise")
		}
		_, err = cleared.Await(scope)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNativeQueryInitCatalogRejectsForeignRetainedAndSubstitutedSettings(t *testing.T) {
	m := backgroundAgentMain(t, "http://127.0.0.1:1")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Identity substitutions are test-only; no background factory reader may
	// still own these fields when the negative admission probe changes them.
	queryCatalogJoinStartupInstallation(t, ctx, m)
	foreign := cooperative.New()
	defer foreign.Close()
	if err := foreign.Run(ctx, func(scope *cooperative.Scope) error {
		_, err := m.beginNativeInitCatalog(scope)
		if !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("foreign init catalog", err)
		}
		original := m.Owner
		m.Owner = foreign
		_, err = m.beginNativeInitCatalog(scope)
		m.Owner = original
		if !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("substituted factory Owner admitted native catalog", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var retained *cooperative.Scope
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		retained = scope
		original := m.nativeSettings
		m.nativeSettings = &NativeProcessSettings{}
		_, err := m.beginNativeInitCatalog(scope)
		m.nativeSettings = original
		if !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("substituted actual settings admitted init metadata", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.beginNativeInitCatalog(retained); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("retained init scope", err)
	}
	finite := &MainPrint{options: MainPrintOptions{Native: &NativeBoundary{}}}
	if finite.nativeInitCatalogProducer() != nil {
		t.Fatal("finite factory acquired native init producer")
	}
}

func queryCatalogDescription(definitions []skillcommands.Definition, name string) string {
	for _, definition := range definitions {
		if definition.Name() == name {
			return definition.Metadata().String("description")
		}
	}
	return ""
}

func queryCatalogJoinStartupInstallation(t *testing.T, ctx context.Context, m *MainPrint) {
	t.Helper()
	if installation := m.nativePluginInstallation; installation != nil {
		// Await the actual private startup producer, without canceling it and
		// poisoning a memoized full-load result needed by the cache assertions.
		select {
		case <-installation.done:
		case <-ctx.Done():
			t.Fatal("startup installation did not quiesce before catalog identity inspection", ctx.Err())
		}
	}
}
