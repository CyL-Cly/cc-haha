package clibootstrap

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/testdata/keychainprefetchfixture"
)

func TestNativeCommandActualKeychainProcessesStartBeforeAdvisorAndSafe(t *testing.T) {
	for _, profile := range []string{"forced-oauth", "disabled-claudeai"} {
		t.Run(profile, func(t *testing.T) {
			f, options := commandKeychainFixture(t)
			if profile == "forced-oauth" {
				f.n.Environment["CLAUDE_CODE_OAUTH_TOKEN"] = "owned-inference-only"
			} else {
				f.n.Environment["ENABLE_CLAUDEAI_MCP_SERVERS"] = "false"
			}
			f.write(t, filepath.Join(f.config, "settings.json"), `{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:2"}}`)
			var mu sync.Mutex
			var order []string
			var processes []*securestorage.KeychainPrefetchProcess
			var admissionFailure error
			f.n.RunSecurity = func(_ context.Context, call securestorage.Command) ([]byte, error) {
				if call.ShellScript == "" {
					return nil, errors.New("owned absent callback")
				}
				mu.Lock()
				defer mu.Unlock()
				if len(processes) != 2 {
					admissionFailure = errors.New("advisor ran before both actual subprocess admissions")
				}
				kind := "sync-legacy"
				if strings.Contains(call.Args[len(call.Args)-1], "-credentials") {
					kind = "sync-oauth"
				}
				order = append(order, kind)
				return nil, errors.New("owned absent sync storage")
			}
			start := keychainprefetchfixture.Bind(f.n.Temp, f.n.CWD, f.n.Home, func(context.Context, securestorage.Command) ([]byte, error) {
				return nil, errors.New("owned absent prefetch")
			})
			f.n.StartKeychainPrefetch = func(ctx context.Context, call securestorage.Command) (*securestorage.KeychainPrefetchProcess, error) {
				process, err := start(ctx, call)
				if err != nil {
					return nil, err
				}
				if process.PID() <= 0 {
					return nil, errors.New("no actual started process")
				}
				mu.Lock()
				kind := "async-legacy"
				if strings.Contains(call.Args[len(call.Args)-1], "-credentials") {
					kind = "async-oauth"
				}
				order = append(order, kind)
				processes = append(processes, process)
				if f.n.Environment["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:1" {
					admissionFailure = errors.New("Safe environment preceded original subprocess launch")
				}
				mu.Unlock()
				return process, nil
			}
			m, err := NewMainCommand(options)
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			mu.Lock()
			got, failure := append([]string(nil), order...), admissionFailure
			actual := append([]*securestorage.KeychainPrefetchProcess(nil), processes...)
			mu.Unlock()
			want := []string{"async-oauth", "async-legacy", "sync-legacy"}
			if profile == "disabled-claudeai" {
				want = append(want, "sync-oauth")
			}
			if failure != nil || !reflect.DeepEqual(got, want) {
				t.Fatal("actual Source pre-main/advisor ordering", got, want, failure)
			}
			if f.n.Environment["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:2" {
				t.Fatal("Safe environment was not applied after callbacks")
			}
			for _, process := range actual {
				select {
				case <-process.Done():
				default:
					t.Fatal("Safe publication preceded actual subprocess callback/Wait", process.PID())
				}
			}
		})
	}
}

func TestNativeCommandCustomSecurityWithoutActualAdmissionFailsClosedBeforeAnyRead(t *testing.T) {
	f, options := commandKeychainFixture(t)
	f.n.StartKeychainPrefetch = nil
	reads := 0
	f.n.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
		reads++
		return nil, errors.New("must never read")
	}
	m, err := NewMainCommand(options)
	if m != nil {
		m.Close()
		t.Fatal("unbound actual primitive constructed command")
	}
	if err == nil || !strings.Contains(err.Error(), "actual subprocess admission primitive") || reads != 0 {
		t.Fatal("custom read callback fell back to real Keychain or advisor", reads, err)
	}
}
