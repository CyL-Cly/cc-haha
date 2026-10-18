package mcptools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpclient"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type inactiveOverlaySnapshot struct {
	Revision         uint64 `json:"revision"`
	CleanupRequested bool   `json:"cleanupRequested"`
	Pending          bool   `json:"pending"`
	NativeCalls      int    `json:"nativeCalls"`
}

func inactiveComputerUseService(t *testing.T) (*Service, *ownerasync.NativeReferences) {
	t.Helper()
	home := t.TempDir()
	owner := cooperative.New()
	t.Cleanup(owner.Close)
	var refs ownerasync.NativeReferences
	binding, err := refs.Bind(owner)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := mcpruntime.New(mcpruntime.Options{CWD: home, Environment: map[string]string{}, Auth: func(context.Context, string, protocol.JSONObject) (mcpclient.AuthProvider, error) { return nil, nil }, Enabled: func(context.Context, string, protocol.JSONObject) (bool, error) {
		t.Error("inactive cleanup probed execution policy")
		return false, nil
	}, Authenticate: func(context.Context, string, protocol.JSONObject, func(string) error) error {
		return errors.New("no private peer")
	}, ClearOAuth: func(context.Context, string, protocol.JSONObject) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	empty := func(context.Context) (protocol.JSONObject, error) { return protocol.NewJSONObject(), nil }
	configs, err := mcpconfig.New(mcpconfig.Options{CWD: home, Platform: "darwin", ManagedRoot: filepath.Join(home, "managed"), ReadFile: func(string) ([]byte, error) { t.Error("inactive cleanup read a file"); return nil, os.ErrNotExist }, Environment: func(string) (string, bool) { return "", false }, SourceEnabled: func(string) bool { return false }, Settings: empty, Policy: empty, Global: empty, Project: empty, Plugins: func(context.Context) (mcpconfig.Result, error) {
		return mcpconfig.Result{Servers: protocol.NewJSONObject()}, nil
	}, Enabled: func(context.Context, string, protocol.JSONObject) (bool, error) { return false, nil }, BypassConfirmed: func(context.Context) (bool, error) { return false, nil }})
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(Options{Owner: owner, NativeBinding: binding, Runtime: runtime, Configurations: configs, PermissionContext: func(*cooperative.Scope) (protocol.JSONObject, error) {
		t.Error("inactive cleanup reconstructed permission state")
		return protocol.NewJSONObject(), nil
	}, ScopeForContext: func(value any) *cooperative.Scope { scope, _ := value.(*cooperative.Scope); return scope }})
	if err != nil {
		t.Fatal(err)
	}
	return s, &refs
}

func TestInactiveComputerUseOverlayReconciliationMatchesActualSourceAndAllocatesNoNativeJob(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Bun required")
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-cu-inactive-cleanup.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "PATH=/usr/bin:/bin", "NODE_ENV=production", "USER_TYPE=external", "CLAUDE_CODE_SIMPLE=1", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_CU_INACTIVE_CLEANUP=1", "BUN_OPTIONS=--no-env-file"}
	command.Stdin = bytes.NewBufferString(`{"home":` + string(raw(home)) + `}`)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatal(err, string(output), stderr.String())
	}
	var source struct {
		Inactive bool
		Overlay  struct {
			First, Second, Settled, Third, Resettled inactiveOverlaySnapshot
			SamePromise, NewPromise                  bool
		}
	}
	if err := json.Unmarshal(output, &source); err != nil || !source.Inactive || !source.Overlay.SamePromise || !source.Overlay.NewPromise {
		t.Fatal("actual Source overlay profile", err, string(output))
	}
	s, refs := inactiveComputerUseService(t)
	if err := s.options.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		snapshot := func() inactiveOverlaySnapshot {
			state := s.inactiveComputerUse
			count, _ := refs.Snapshot()
			return inactiveOverlaySnapshot{state.overlayRevision, state.overlayCleanupRequested, state.overlayReconcilePromise != nil, count}
		}
		one, err := s.hideInactiveComputerUseOverlay(scope)
		if err != nil {
			return err
		}
		first := snapshot()
		two, err := s.hideInactiveComputerUseOverlay(scope)
		if err != nil {
			return err
		}
		second := snapshot()
		if one != two {
			t.Error("concurrent overlayHide did not share actual reconciliation")
		}
		if _, err = one.Await(scope); err != nil {
			return err
		}
		settled := snapshot()
		three, err := s.hideInactiveComputerUseOverlay(scope)
		if err != nil {
			return err
		}
		third := snapshot()
		if three == one {
			t.Error("finally did not retire reconciliation")
		}
		if _, err = three.Await(scope); err != nil {
			return err
		}
		actual := []inactiveOverlaySnapshot{first, second, settled, third, snapshot()}
		expected := []inactiveOverlaySnapshot{source.Overlay.First, source.Overlay.Second, source.Overlay.Settled, source.Overlay.Third, source.Overlay.Resettled}
		if !reflect.DeepEqual(actual, expected) {
			t.Errorf("actual overlay lifetime Go=%+v Source=%+v", actual, expected)
		}
		for range 2 {
			if err = s.EndTurnOwner(scope); err != nil {
				return err
			}
		}
		if state := snapshot(); state.Revision != 5 || state.Pending || state.NativeCalls != 0 || s.HasComputerUse() {
			t.Error("inactive default cleanup retained work or activated CU", state)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestInactiveComputerUseNativeCleanupUsesIndependentSignalAndRejectsForeignOwner(t *testing.T) {
	s, refs := inactiveComputerUseService(t)
	foreign := cooperative.New()
	defer foreign.Close()
	if err := foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		if err := s.EndTurnOwner(scope); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("foreign cleanup admitted", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	caller, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.options.Owner.Run(caller, func(scope *cooperative.Scope) error {
		authority, err := cooperative.NewNativeComputerUseCleanup(scope, s.options.Owner)
		if err != nil {
			return err
		}
		cancel()
		return authority.Run(scope, func(cleanup *cooperative.Scope, signal context.Context) error {
			return s.EndTurnOwnerContext(cleanup, signal)
		})
	}); err != nil {
		t.Fatal(err)
	}
	if count, _ := refs.Snapshot(); count != 0 {
		t.Fatal("unused CU allocated physical native job", count)
	}
}
