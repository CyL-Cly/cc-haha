package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestNativeMainAndScopedSettingsPathProducerAgainstWholeSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun required")
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-config-directory-runtime.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("path consumer reached model", r.URL)
		http.Error(w, "unexpected model", 500)
	}))
	defer peer.Close()
	var source struct {
		Initial, Redirected, Project string
		After                        struct{ Credential json.RawMessage }
		Paths                        []struct {
			Name, Cwd, Path, KeychainService, KeychainAccount, LegacyService string
			Directory, User                                                  *string
			Cowork                                                           bool
		}
		Caps struct{ Blocked, SecureReads []json.RawMessage }
	}
	var observedService atomic.Value
	observedService.Store("")
	var observedAccount atomic.Value
	observedAccount.Store("")
	var observedLegacy atomic.Value
	observedLegacy.Store(securestorage.Command{})
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		n.RunSecurity = func(_ context.Context, request securestorage.Command) ([]byte, error) {
			if request.ShellScript != "" {
				observedLegacy.Store(request)
			}
			if len(request.Args) == 6 && request.Args[0] == "find-generic-password" {
				observedService.Store(request.Args[5])
				observedAccount.Store(request.Args[2])
			}
			return nil, os.ErrNotExist
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bun, "--feature=TRANSCRIPT_CLASSIFIER", "--no-env-file", fixture)
		cmd.Dir = n.Home
		cmd.Env = []string{"HOME=" + n.Home, "USERPROFILE=" + n.Home, "CLAUDE_CONFIG_DIR=" + filepath.Join(n.Home, ".claude"), "TMPDIR=" + n.Home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(n.Home), "CC_HAHA_CONFIG_DIRECTORY_SOURCE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "USER_TYPE=external", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
		cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"home": n.Home, "warmProject": true}))
		output, err := cmd.CombinedOutput()
		if err != nil || json.Unmarshal(output, &source) != nil || len(source.Paths) != 6 || len(source.Caps.Blocked) != 0 || len(source.Caps.SecureReads) != 0 {
			t.Fatalf("whole original settings path producer %v %.6500s", err, output)
		}
		// Startup's dynamic-directory guard remains separately open. Remove
		// only this fixture env producer while testing its real path consumers.
		if err := os.WriteFile(filepath.Join(source.Initial, "settings.json"), []byte(`{"language":"old-location","future":{"keep":41}}`), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(source.Redirected, ".credentials.json"), source.After.Credential, 0600); err != nil {
			t.Fatal(err)
		}
		n.CWD = source.Paths[0].Cwd
	}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		for _, row := range source.Paths {
			var user json.RawMessage
			if row.User != nil {
				user = quoteMain(*row.User)
			}
			if err := m.environment.SetProperty(quoteMain("USER"), user); err != nil {
				return err
			}
			if row.Directory == nil {
				// The scalar seam represents undefined explicitly. The path
				// getter observes the same undefined as Source's deleted key.
				if err := m.environment.SetProperty(quoteMain("CLAUDE_CONFIG_DIR"), nil); err != nil {
					return err
				}
			} else if err := m.environment.SetProperty(quoteMain("CLAUDE_CONFIG_DIR"), quoteMain(*row.Directory)); err != nil {
				return err
			}
			cowork := ""
			if row.Cowork {
				cowork = "true"
			}
			if err := m.environment.SetProperty(quoteMain("CLAUDE_CODE_USE_COWORK_PLUGINS"), quoteMain(cowork)); err != nil {
				return err
			}
			path, err := m.settings.Path("userSettings", "")
			if err != nil || path != row.Path {
				return fmt.Errorf("Main settings %s path %q Source %q: %v", row.Name, path, row.Path, err)
			}
			m.secure.Clear()
			if _, err := m.secure.ReadSourceJSON(ctx); err != nil || observedService.Load() != row.KeychainService {
				return fmt.Errorf("Main actual Keychain service %s %q Source %q: %v", row.Name, observedService.Load(), row.KeychainService, err)
			}
			if observedAccount.Load() != row.KeychainAccount {
				return fmt.Errorf("Main actual Keychain account %s %q Source %q", row.Name, observedAccount.Load(), row.KeychainAccount)
			}
			_, _ = m.secure.ReadLegacyManagedAPIKey(ctx)
			legacy := observedLegacy.Load().(securestorage.Command)
			if len(legacy.Args) < 5 || legacy.Args[len(legacy.Args)-1] != row.LegacyService || legacy.ShellScript == "" {
				return fmt.Errorf("Main actual legacy service %s %+v Source %q", row.Name, legacy, row.LegacyService)
			}
			directory, environment := m.nativeSecurityContext()
			if directory != row.Cwd {
				return fmt.Errorf("Main current security cwd %s %q Source %q", row.Name, directory, row.Cwd)
			}
			if user, present := environment["USER"]; present != (row.User != nil) || row.User != nil && user != *row.User {
				return fmt.Errorf("Main security lost actual USER %s %q/%v", row.Name, user, present)
			}
			if row.Name == "absolute" {
				credential, err := m.secure.ReadSourceJSON(ctx)
				if err != nil || !bytes.Equal(credential, source.After.Credential) {
					return fmt.Errorf("Main plaintext fallback retained old directory: %s Source %s: %v", credential, source.After.Credential, err)
				}
			}
		}
		if _, err := m.Startup.GetInitialSettings(); err == nil || !strings.Contains(err.Error(), "changed filesystem/schema host CLAUDE_CONFIG_DIR") {
			return fmt.Errorf("Main frozen host guard was expanded before other consumers: %v", err)
		}
		if err := m.environment.SetProperty(quoteMain("CLAUDE_CONFIG_DIR"), quoteMain(source.Initial)); err != nil {
			return err
		}
		return m.environment.SetProperty(quoteMain("CLAUDE_CODE_USE_COWORK_PLUGINS"), quoteMain(""))
	}); err != nil {
		t.Fatal(err)
	}
	// Scoped settings have their own environment authority. Binding to the
	// launch map would make later scoped reads use the parent's stale value.
	p, err := NewNativeProcessSettings(m.options.Native.agentScopedBoundary(m.options.Native.CWD), "darwin", DefaultState())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close(ctx)
	if err := p.ensureSettings(); err != nil {
		t.Fatal(err)
	}
	if err := p.environment.SetProperty(quoteMain("CLAUDE_CONFIG_DIR"), quoteMain(source.Redirected)); err != nil {
		t.Fatal(err)
	}
	path, err := p.settings.Path("userSettings", "")
	if err != nil || path != filepath.Join(source.Redirected, "settings.json") {
		t.Fatal("scoped settings retained launch environment", path, err)
	}
}
