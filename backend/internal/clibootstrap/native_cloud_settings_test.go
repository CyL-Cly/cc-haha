package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/authhelper"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualMainRetainsInactiveCloudCommandsAndSettingsChangeClearsBothRealCaches(t *testing.T) {
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		emitPlanFixture(w, 1, "", "DIRECT_PROVIDER_DONE")
	}))
	defer peer.Close()
	var marker, config, command string
	settings := func(revision int) map[string]any {
		return map[string]any{"agentTeamsEnabled": false, "alwaysThinkingEnabled": false, "awsCredentialExport": command, "awsAuthRefresh": command, "gcpAuthRefresh": command, "future": map[string]any{"preserved": revision}}
	}
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		delete(n.Environment, "CLAUDE_CODE_SIMPLE")
		marker = filepath.Join(n.CWD, "cloud-command-must-not-run")
		script := filepath.Join(n.CWD, "unused-cloud-command.py")
		if err := os.WriteFile(script, []byte(fmt.Sprintf("from pathlib import Path\nPath(%q).write_text('unexpected cloud auth')\nprint('null')\n", marker)), 0600); err != nil {
			t.Fatal(err)
		}
		command = "/usr/bin/python3 " + settingsShellQuote(script)
		config = filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json")
		if err := os.WriteFile(config, rawMain(settings(1)), 0600); err != nil {
			t.Fatal(err)
		}
	}, func(o *MainPrintOptions) { o.Arguments.Tools = []string{"Write,Read"} })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "direct provider with dormant old cloud configuration"})})
	if err := m.RunTurn(ctx, user); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("direct provider executed unused cloud auth command", err)
	}
	// Populate the actual two memo instances through real private OS work. This
	// tests watcher invalidation, without pretending STS/ADC was implemented.
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		for _, cache := range []*authhelper.CloudCache{m.awsCredentials, m.gcpCredentials} {
			lookup, err := cache.Begin(func(active context.Context) (json.RawMessage, error) {
				value, err := authhelper.Execute(active, "printf null", authhelper.ProcessOptions{CWD: m.options.Native.CWD, Environment: map[string]string{"HOME": m.options.Native.Home, "PATH": "/usr/bin:/bin"}})
				return json.RawMessage(value), err
			})
			if err != nil {
				return err
			}
			future, err := nativeSkillWork(m, scope, func() (json.RawMessage, error) { return lookup.Wait(ctx) })
			if err != nil {
				return err
			}
			if _, err = future.Await(scope); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, rawMain(settings(2)), 0600); err != nil {
		t.Fatal(err)
	}
	waitNativeSettings(t, func() bool {
		ready := false
		receipt, err := m.Owner.Submit(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
			actual, _, err := m.Controller.SDKSettingsOnOwner(scope)
			if err != nil {
				return err
			}
			future, _ := actual.Object("future")
			_, aws := m.awsCredentials.Cached()
			_, gcp := m.gcpCredentials.Cached()
			ready = string(future.Get("preserved")) == "2" && !aws && !gcp && actual.String("awsCredentialExport") == command && actual.String("gcpAuthRefresh") == command
			return nil
		})
		if err == nil {
			err = receipt.Wait()
		}
		if err != nil {
			t.Fatal(err)
		}
		return ready
	})
	if err := m.RunTurn(ctx, user); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("settings/cache change created hidden model requests", calls.Load())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("settings/cache change executed dormant cloud command", err)
	}
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		if err := m.environment.Assign(protocol.NewJSONObject(protocol.JSONField{Name: "CLAUDE_CODE_USE_BEDROCK", Value: quoteMain("1")})); err != nil {
			return err
		}
		if m.nativeCloudSettingsInactive(scope) {
			return fmt.Errorf("active cloud SDK/STS consumer was incorrectly advertised migrated")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
}
