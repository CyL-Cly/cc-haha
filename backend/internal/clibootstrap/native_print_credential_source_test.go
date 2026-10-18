package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

type nativePrintFDProfile struct {
	Name        string            `json:"name"`
	Environment map[string]string `json:"env"`
	Files       map[string]string `json:"files"`
	Interactive bool              `json:"interactive"`
}

type nativePrintFDSnapshot struct {
	Key                 string `json:"key"`
	Source              string `json:"source"`
	Tokens              any    `json:"tokens"`
	Subscriber          bool   `json:"subscriber"`
	TokenSource         string `json:"tokenSource"`
	AccountAPIKeySource string `json:"accountAPIKeySource"`
}

func TestNativePrintFDOriginalSourceCacheMetadataAndBorrowedConsumers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("inherited Unix descriptor regression")
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	rows := []nativePrintFDProfile{
		{"api-pipe", map[string]string{"CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR": "  +3tail"}, map[string]string{"descriptor-3": " \ufefffake-pipe-api\u00a0\n"}, false},
		{"oauth-pipe", map[string]string{"CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR": "4tail"}, map[string]string{"descriptor-4": " \ufefffake-pipe-oauth\u00a0\n"}, false},
		{"both-independent-slots", map[string]string{"CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR": "3", "CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR": "4"}, map[string]string{"descriptor-3": "fake-both-api", "descriptor-4": "fake-both-oauth"}, false},
		{"print-env-before-api-fd", map[string]string{"ANTHROPIC_API_KEY": "fake-print-env", "CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR": "3"}, map[string]string{"descriptor-3": "fake-lower-api"}, false},
		{"ci-interactive-api-fd-before-env", map[string]string{"CI": "1", "ANTHROPIC_API_KEY": "fake-lower-env", "CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR": "3"}, map[string]string{"descriptor-3": "fake-ci-pipe"}, true},
		{"oauth-env-before-fd", map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "fake-oauth-env", "CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR": "4"}, map[string]string{"descriptor-4": "fake-lower-oauth"}, false},
		{"bare-ignores-both-fd", map[string]string{"CLAUDE_CODE_SIMPLE": "1", "ANTHROPIC_API_KEY": "fake-bare-env", "CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR": "3", "CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR": "4"}, map[string]string{"descriptor-3": "fake-ignored-api", "descriptor-4": "fake-ignored-oauth"}, false},
		{"invalid-api-caches-null-no-fallback", map[string]string{"CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR": "invalid"}, map[string]string{".api_key": "fake-must-ignore-invalid-fallback"}, false},
		{"empty-oauth-caches-null-no-fallback", map[string]string{"CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR": "4"}, map[string]string{"descriptor-4": " \n", ".oauth_token": "fake-must-ignore-empty-fallback"}, false},
		{"failed-oauth-fd-uses-private-fallback", map[string]string{"CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR": "999999"}, map[string]string{".oauth_token": " fake-io-fallback\n"}, false},
		{"no-fd-private-api-fallback", map[string]string{}, map[string]string{".api_key": " fake-api-fallback\n"}, false},
		{"no-fd-private-oauth-fallback", map[string]string{}, map[string]string{".oauth_token": " fake-oauth-fallback\n"}, false},
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture := os.Getenv("CC_HAHA_PRINT_FD_SOURCE_FIXTURE")
	if fixture == "" {
		fixture, err = filepath.Abs("../../../scripts/quality-gate/go-parity/native-print-fd-credentials.fixture.ts")
	} else if !filepath.IsAbs(fixture) {
		t.Fatal("explicit Source fixture must be absolute")
	}
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_PRINT_FD_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "USER_TYPE=external", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH=1", "PATH=/usr/bin:/bin"}
	command.Stdin = bytes.NewReader(rawMain(rows))
	var stderr bytes.Buffer
	command.Stderr = &stderr
	data, err := command.Output()
	if err != nil {
		t.Fatalf("actual Source FD metadata: %v %s", err, stderr.String())
	}
	var expected []struct {
		Before, After   nativePrintFDSnapshot
		DescriptorReads map[string]int
	}
	if json.Unmarshal(data, &expected) != nil || len(expected) != len(rows) {
		t.Fatalf("actual Source shape %s", data)
	}
	for i, row := range rows {
		t.Run(row.Name, func(t *testing.T) {
			var readers []*os.File
			m := openAIAuthMainFixtureWithOptions(t, nil, func(n *NativeBoundary) {
				delete(n.Environment, "ANTHROPIC_API_KEY")
				delete(n.Environment, "ANTHROPIC_AUTH_TOKEN")
				for key, value := range row.Environment {
					n.Environment[key] = value
				}
				n.CCRCredentialDirectory = filepath.Join(n.Temp, "remote-private")
				if err := os.MkdirAll(n.CCRCredentialDirectory, 0700); err != nil {
					t.Fatal(err)
				}
				for name, value := range row.Files {
					if strings.HasPrefix(name, "descriptor-") {
						reader, writer, err := os.Pipe()
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { _ = reader.Close() })
						if _, err := writer.WriteString(value); err != nil {
							_ = writer.Close()
							t.Fatal(err)
						}
						if err := writer.Close(); err != nil {
							t.Fatal(err)
						}
						for _, key := range []string{"CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR", "CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR"} {
							if strings.Contains(row.Environment[key], strings.TrimPrefix(name, "descriptor-")) {
								n.Environment[key] = strconv.FormatUint(uint64(reader.Fd()), 10) + "tail"
							}
						}
						readers = append(readers, reader)
					} else if err := os.WriteFile(filepath.Join(n.CCRCredentialDirectory, name), []byte(value), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}, func(options *MainPrintOptions) { options.Interactive = row.Interactive })
			snapshot := func() nativePrintFDSnapshot {
				t.Helper()
				var result nativePrintFDSnapshot
				err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
					env := m.nativeOpenAIEnvironment()
					owned, err := m.openAIAuth.owned(ctx, scope)
					if err != nil {
						return err
					}
					result.Source, err = m.openAIAuth.apiKeySource(owned, env, envTruthyNative(env["CLAUDE_CODE_SIMPLE"]), !row.Interactive)
					if err != nil {
						return err
					}
					helper, err := m.nativeAuthHelperBinding(scope)
					if err != nil {
						return err
					}
					resolved, err := helper.Resolve(ctx, !row.Interactive)
					if err != nil {
						return err
					}
					if resolved.Key != nil {
						result.Key = *resolved.Key
					}
					tokens, err := m.openAIAuth.claudeTokensFor(owned, env, envTruthyNative(env["CLAUDE_CODE_SIMPLE"]))
					if err != nil {
						return err
					}
					if tokens != nil {
						if err := json.Unmarshal(rawMain(tokens.Object), &result.Tokens); err != nil {
							return err
						}
					}
					identity, err := m.openAIAuth.AnthropicModelIdentity(ctx, scope, !row.Interactive)
					if err != nil {
						return err
					}
					result.Subscriber = identity.Subscriber
					account, err := m.nativeSDKAccount(scope, env, identity)
					result.TokenSource, result.AccountAPIKeySource = account.String("tokenSource"), account.String("apiKeySource")
					return err
				})
				if err != nil {
					t.Fatal(err)
				}
				return result
			}
			before := snapshot()
			for _, reader := range readers {
				_ = reader.Close()
			}
			for _, name := range []string{".api_key", ".oauth_token"} {
				if err := os.WriteFile(filepath.Join(m.options.Native.CCRCredentialDirectory, name), []byte("fake-later-must-not-be-adopted"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := m.Owner.Run(ctx, m.openAIAuth.ClearClaudeTokenCache); err != nil {
				t.Fatal(err)
			}
			after := snapshot()
			if !reflect.DeepEqual(before, expected[i].Before) || !reflect.DeepEqual(after, expected[i].After) || !reflect.DeepEqual(before, after) {
				t.Fatalf("Source before=%+v after=%+v; Go before=%+v after=%+v", expected[i].Before, expected[i].After, before, after)
			}
			if row.Name == "both-independent-slots" {
				for _, n := range []*NativeBoundary{m.options.Native, m.options.Native.agentScopedBoundary(m.options.Native.CWD)} {
					borrowed := &MainCommand{Native: n, credentialPlatform: m.options.Platform, credentialDirectory: n.CCRCredentialDirectory, credentialCache: map[string]string{}}
					if err := m.Owner.Run(ctx, func(*cooperative.Scope) error {
						if borrowed.credentialFromDescriptor("CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR", ".api_key") != "fake-both-api" || borrowed.credentialFromDescriptor("CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR", ".oauth_token") != "fake-both-oauth" {
							return fmt.Errorf("borrowed command or scoped Agent re-read a drained inherited pipe")
						}
						return nil
					}); err != nil {
						t.Fatal(err)
					}
				}
			}
			m.options.Native.mu.Lock()
			events := append([]NativeEvent(nil), m.options.Native.Trace...)
			m.options.Native.mu.Unlock()
			reads := map[string]int{}
			for _, event := range events {
				if event.Operation == "readFile" && (strings.HasPrefix(event.Path, "/dev/fd/") || strings.HasPrefix(event.Path, "/proc/self/fd/")) {
					reads[event.Path]++
				}
			}
			for path, count := range reads {
				if count != 1 {
					t.Fatalf("descriptor re-read %s %d", path, count)
				}
			}
			if row.Name == "bare-ignores-both-fd" && len(reads) != 0 {
				t.Fatal("bare read inherited descriptors", reads)
			}
			for path, count := range expected[i].DescriptorReads {
				if count != 1 {
					t.Fatal("Source descriptor re-read", path, count)
				}
			}
		})
	}
}
