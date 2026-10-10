package clibootstrap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamworker"
)

func TestNativeTeamExecutionProofNeedsActualWorkerOwnerAndRoleBinding(t *testing.T) {
	names := []string{"Read"}
	m, _, _ := teamWorkerFixture(t, teamworker.Start{ParentSessionID: "parent", TeamName: "actual", MemberID: "m", Name: "worker", SystemPrompt: "actual main", Tools: &names, AgentDefinition: protocol.NewJSONObject()}, "http://127.0.0.1:1")
	if err := m.StartSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	foreign := cooperative.New()
	defer foreign.Close()
	var stale *cooperative.Scope
	var worker *sdkhost.NativeTeamWorker
	if err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		stale = scope
		var err error
		worker, err = m.nativeTeamWorkerBinding(scope)
		if err != nil {
			return err
		}
		proof, err := sdkhost.NewNativeTeamExecution(scope, m.Owner, m.Controller, m.nativeTools, worker, m.options.Native.Environment)
		if err != nil || proof == nil {
			return fmt.Errorf("real worker proof missing %v", err)
		}
		proof, err = sdkhost.NewNativeTeamExecution(scope, m.Owner, m.Controller, m.nativeTools, nil, m.options.Native.Environment)
		if !errors.Is(err, cooperative.ErrRestricted) || proof != nil {
			return fmt.Errorf("worker identity minted an ordinary leader proof %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := sdkhost.NewNativeTeamExecution(stale, m.Owner, m.Controller, m.nativeTools, worker, m.options.Native.Environment); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatalf("stale mint %v", err)
	}
	if err := foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		if _, err := sdkhost.NewNativeTeamExecution(scope, foreign, m.Controller, m.nativeTools, worker, m.options.Native.Environment); !errors.Is(err, cooperative.ErrRestricted) {
			return fmt.Errorf("foreign bindings minted %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNativeEnabledTeamsOrdinarySessionUsesInstalledProducerConsumerBindings(t *testing.T) {
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false},"agentTeamsEnabled":true}`)
	f.n.Environment["ANTHROPIC_API_KEY"] = "fake-private-key"
	f.n.Environment["ANTHROPIC_BASE_URL"] = "http://127.0.0.1:1"
	f.n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
	disabled := false
	m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "actual", Tools: []string{"Read"}, Sources: DefaultState(), PermissionMode: "default", MaxTurns: 8}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	defer m.ShutdownSession(context.Background())
	if err = m.StartSession(context.Background()); err != nil {
		t.Fatal("complete ordinary Teams graph rejected", err)
	}
	if err = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		flagOnly, err := sdkhost.NewNativeTeamExecution(scope, m.Owner, m.Controller, nil, nil, map[string]string{"CC_HAHA_AGENT_TEAMS_ENABLED": "1"})
		if err != nil || flagOnly != nil {
			return fmt.Errorf("environment alone minted executable Teams %v", err)
		}
		proof, err := sdkhost.NewNativeTeamExecution(scope, m.Owner, m.Controller, m.nativeTools, nil, m.options.Native.Environment)
		if err != nil || proof == nil || !m.nativeTools.HasTeamExecutionBindings() {
			return fmt.Errorf("ordinary actual producer proof missing %v", err)
		}
		_, err = m.Host.NativePermissionContext(scope)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
