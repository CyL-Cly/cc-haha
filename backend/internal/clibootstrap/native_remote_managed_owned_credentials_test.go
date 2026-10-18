package clibootstrap

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

// Enter through the actual native constructor: safe settings eligibility runs
// before Controller exists. Only the external Keychain primitive is replaced;
// the real secure store, memo, private global config and error log are consumed.
func remoteManagedOwnedCredentialMain(t *testing.T, fallback string, subscriber bool) (*MainPrint, *atomic.Int32, *bytes.Buffer) {
	t.Helper()
	f := newNativePluginFixture(t)
	delete(f.n.Environment, "ANTHROPIC_BASE_URL")
	f.n.Environment["NODE_ENV"] = "production"
	f.n.Environment["CLAUDE_CODE_TMPDIR"] = f.n.Temp
	f.write(t, filepath.Join(f.config, "settings.json"), `{}`)
	f.write(t, filepath.Join(f.config, ".claude.json"), string(rawMain(map[string]any{"primaryApiKey": fallback})))
	if subscriber {
		f.write(t, filepath.Join(f.config, ".credentials.json"), `{"claudeAiOauth":{"accessToken":"private-pro-token","scopes":["user:inference"],"subscriptionType":"pro"}}`)
	}
	calls := new(atomic.Int32)
	failure := errors.New("private remote eligibility legacy lookup unavailable")
	f.n.RunSecurity = func(ctx context.Context, command securestorage.Command) ([]byte, error) {
		// Primary OAuth-prefetch and sync-read calls have their own real private
		// process transport. Distinguish the actual service identity, not a counter.
		service := ""
		for index, argument := range command.Args {
			if argument == "-s" && index+1 < len(command.Args) {
				service = command.Args[index+1]
			}
		}
		if strings.Contains(service, "-credentials") {
			return nil, nil
		}
		if service == "" {
			return nil, fmt.Errorf("missing private Keychain service")
		}
		calls.Add(1)
		return nil, failure
	}
	var terminal bytes.Buffer
	disabled := false
	m, err := NewMainPrint(MainPrintOptions{
		Native: f.n, Platform: "darwin", PID: os.Getpid(), Version: "2.1.288",
		Input: strings.NewReader(""), Output: io.Discard, Error: &terminal,
		Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", Sources: DefaultState(), SystemPrompt: "private credential constructor without inference", Tools: []string{""}, MaxTurns: 1},
	})
	if err != nil {
		t.Fatalf("actual native constructor rejected recoverable legacy lookup: %v (legacy calls=%d)", err, calls.Load())
	}
	t.Cleanup(m.Close)
	return m, calls, &terminal
}

func TestRemoteManagedConstructorLegacyFailureRetainsOwnedFallbackAndMemo(t *testing.T) {
	for _, profile := range []struct {
		name, fallback string
		subscriber     bool
	}{
		{name: "no-account"}, {name: "private-config-fallback", fallback: "private-managed-fallback"}, {name: "pro-subscriber", subscriber: true},
	} {
		t.Run(profile.name, func(t *testing.T) {
			m, calls, terminal := remoteManagedOwnedCredentialMain(t, profile.fallback, profile.subscriber)
			if calls.Load() != 1 {
				t.Fatalf("constructor legacy reads=%d", calls.Load())
			}
			if m.errorLog == nil {
				t.Fatal("recoverable credential failure missed actual error logger")
			}
			rows := m.errorLog.Memory()
			if len(rows) != 1 || rows[0].Error != "private remote eligibility legacy lookup unavailable" {
				t.Fatalf("actual privacy logger rows=%+v", rows)
			}
			if err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				if !m.openAIAuth.legacyAPIKeyLoaded || m.openAIAuth.legacyAPIKey != profile.fallback {
					return fmt.Errorf("constructor did not retain actual legacy memo/config fallback")
				}
				for _, eligibility := range []bool{true, false, true} {
					credentials, err := m.sampleRemoteManagedCredentials(scope, eligibility)
					if err != nil {
						return err
					}
					if credentials.APIKey != profile.fallback {
						return fmt.Errorf("credential fallback changed: %q", credentials.APIKey)
					}
					if profile.subscriber && (credentials.OAuth == nil || credentials.OAuth.String("subscriptionType") != "pro") {
						return fmt.Errorf("pro subscriber was erased during legacy fallback")
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 1 || len(m.errorLog.Memory()) != 1 {
				t.Fatal("warm sampling repeated legacy lookup/reporting")
			}
			if terminal.Len() != 0 {
				t.Fatalf("recoverable credential failure leaked to terminal: %q", terminal.String())
			}
		})
	}
}

func TestRemoteManagedCredentialSamplingKeepsScopeAndLifetimeAuthority(t *testing.T) {
	m, calls, _ := remoteManagedOwnedCredentialMain(t, "", false)
	if _, err := m.sampleRemoteManagedCredentials(nil, true); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatalf("nil scope admitted: %v", err)
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if err := foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		if _, err := m.sampleRemoteManagedCredentials(scope, true); !errors.Is(err, cooperative.ErrRestricted) {
			return fmt.Errorf("foreign scope admitted: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var retained *cooperative.Scope
	if err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error { retained = scope; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := m.sampleRemoteManagedCredentials(retained, true); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatalf("retained scope admitted: %v", err)
	}
	m.remoteManaged.cancel()
	if err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if _, err := m.sampleRemoteManagedCredentials(scope, true); !errors.Is(err, context.Canceled) {
			return fmt.Errorf("canceled lifetime was not preserved: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || len(m.errorLog.Memory()) != 1 {
		t.Fatal("rejected scope/lifetime performed another credential lookup/report")
	}
}
