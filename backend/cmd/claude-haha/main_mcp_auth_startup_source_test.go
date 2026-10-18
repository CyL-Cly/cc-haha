package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

// This is intentionally a whole-main comparison. The catalog's zero-read
// branch is not evidence that Source's independent startup prefetch was absent.
// Async prefetch, synchronous advisor lookups and catalog reads remain separate.
func TestMCPCLIWholePreActionCredentialCountsAgainstActualSource(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("actual Source Keychain prefetch is Darwin-only")
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("pinned actual Source Bun required")
	}
	fixture, err := filepath.Abs("../../testdata/native-mcp-auth-startup.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	inputs := []string{"src/main.tsx", "src/entrypoints/init.ts", "src/utils/auth.ts", "src/utils/advisor.ts", "src/utils/betas.ts", "src/utils/model/providers.ts", "src/utils/envUtils.ts", "src/utils/secureStorage/keychainPrefetch.ts", "src/utils/secureStorage/macOsKeychainHelpers.ts", "src/utils/secureStorage/macOsKeychainStorage.ts", "src/utils/secureStorage/fallbackStorage.ts", "src/utils/secureStorage/plainTextStorage.ts", "src/utils/secureStorage/index.ts", "src/cli/handlers/mcp.tsx", "src/services/mcp/claudeai.ts", "src/utils/sinks.ts", "scripts/quality-gate/go-parity/client-options-capabilities.ts", "scripts/quality-gate/go-parity/fixture-guard.ts", "preload.ts", "src/utils/execFileNoThrowPortable.ts", "node_modules/execa/package.json", "node_modules/execa/lib/methods/main-sync.js", "node_modules/execa/lib/arguments/options.js", "node_modules/execa/lib/resolve/exit-sync.js", "node_modules/execa/lib/io/output-sync.js", "backend/testdata/native-mcp-auth-startup.fixture.ts", "tsconfig.json"}
	identity := func() map[string][32]byte {
		result := map[string][32]byte{}
		for _, name := range inputs {
			data, failure := os.ReadFile(filepath.Join(root, name))
			if failure != nil {
				t.Fatal(failure)
			}
			result[name] = sha256.Sum256(data)
		}
		return result
	}
	before := identity()
	for _, profile := range []string{"flag-bare", "env-simple", "forced-oauth", "disabled-claudeai"} {
		t.Run(profile, func(t *testing.T) {
			home, failure := filepath.EvalSymlinks(t.TempDir())
			if failure != nil {
				t.Fatal(failure)
			}
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodHead {
					t.Error("unexpected actual Source network request", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusForbidden)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer peer.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			command.Dir = home
			command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CLAUDE_CODE_TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_MCP_AUTH_STARTUP_SOURCE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "USER_TYPE=external", "NO_COLOR=1", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH=1", "ANTHROPIC_BASE_URL=" + peer.URL}
			input, _ := json.Marshal(map[string]string{"home": home, "origin": peer.URL, "profile": profile})
			command.Stdin = bytes.NewReader(input)
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			if failure := command.Run(); failure != nil {
				t.Fatalf("whole original main setup/caller failed: %v stdout%q stderr%q", failure, stdout.String(), stderr.String())
			}
			if stdout.String() != "No MCP servers configured. Use `claude mcp add` to add a server.\n" || stderr.Len() != 0 {
				t.Fatal("actual MCP public result changed", stdout.String(), stderr.String())
			}
			type catalogCounts struct{ Physical, Logical, Getter int }
			var source struct {
				Code       int
				Prefetched []struct {
					Command        string
					Args           []string
					Timeout, Code  int
					Signal         *string
					Stdout, Stderr string
				}
				SyncCalls []struct {
					Kind, Command string
					Admitted      bool
					Error         string
					Result        struct {
						Status          int
						Signal          *string
						OutputNullStdin bool
						Stdout, Stderr  struct {
							IsBuffer bool
							Bytes    int
							UTF8     string
						}
					}
				}
				LogicalReads []struct {
					Method        string
					CachedAt      int64
					Generation    int
					CacheDataNull bool
				}
				StorageGetters              int
				CatalogBefore, CatalogAfter *catalogCounts
				Caps                        struct{ Blocked, SecureReads []string }
			}
			data, failure := os.ReadFile(filepath.Join(home, "mcp-auth-startup-source.json"))
			if failure != nil || json.Unmarshal(data, &source) != nil || source.Code != 0 || len(source.Caps.Blocked) != 0 || source.CatalogBefore == nil || source.CatalogAfter == nil {
				t.Fatalf("missing valid whole-source credential receipt: %v %s", failure, data)
			}
			if *source.CatalogAfter != *source.CatalogBefore {
				t.Fatal("Source catalog unexpectedly read credentials", string(data))
			}
			if len(source.Caps.SecureReads) != 0 {
				t.Fatal("mock SecureStorage was used instead of original cache/getter", string(data))
			}
			for _, call := range source.Prefetched {
				if call.Command != "security" || len(call.Args) != 6 || call.Args[0] != "find-generic-password" || call.Args[1] != "-a" || call.Args[2] != "owned-mcp-fixture" || call.Args[3] != "-w" || call.Args[4] != "-s" || !strings.HasPrefix(call.Args[5], "Claude Code") || call.Timeout != 10000 || call.Code != 44 || call.Signal != nil || call.Stdout != "" || call.Stderr != "" {
					t.Fatal("missing actual owned absent-Keychain process receipt", string(data))
				}
			}
			wantKinds := map[string]int{}
			for _, call := range source.Prefetched {
				kind := "async-legacy"
				if strings.Contains(call.Args[5], "-credentials") {
					kind = "async-oauth"
				}
				wantKinds[kind]++
			}
			for _, call := range source.SyncCalls {
				if !call.Admitted || call.Error != "" || call.Kind != "legacy" && call.Kind != "oauth" || call.Result.Status != 44 || call.Result.Signal != nil || !call.Result.OutputNullStdin || !call.Result.Stdout.IsBuffer || call.Result.Stdout.Bytes != 0 || call.Result.Stdout.UTF8 != "" || !call.Result.Stderr.IsBuffer || call.Result.Stderr.Bytes != 0 || call.Result.Stderr.UTF8 != "" {
					t.Fatal("missing actual synchronous owned exit-44 Buffer receipt", string(data))
				}
				wantKinds["sync-"+call.Kind]++
			}
			n, _ := mcpCLIPrivateNative(t)
			n.Environment["CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH"] = "1"
			n.Environment["USER"] = "owned-mcp-fixture"
			n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
			args := []string{"mcp", "list"}
			switch profile {
			case "flag-bare":
				args = append([]string{"--bare"}, args...)
			case "env-simple":
				n.Environment["CLAUDE_CODE_SIMPLE"] = "1"
			case "forced-oauth":
				n.Environment["CLAUDE_CODE_OAUTH_TOKEN"] = "private-inference-only"
			case "disabled-claudeai":
				n.Environment["ENABLE_CLAUDEAI_MCP_SERVERS"] = "false"
			}
			var reads []securestorage.Command
			var readsMu sync.Mutex
			n.RunSecurity = func(owned context.Context, call securestorage.Command) ([]byte, error) {
				readsMu.Lock()
				reads = append(reads, call)
				readsMu.Unlock()
				// The same owned empty executable supplies the actual exit-44
				// primitive receipt to both runtimes; no real Keychain call.
				securityCommand := exec.CommandContext(owned, filepath.Join(home, "private-empty-security.sh"), call.Args...)
				securityCommand.Dir = home
				securityCommand.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "PATH=/usr/bin:/bin", "TMPDIR=" + home}
				return securityCommand.Output()
			}
			n.StartKeychainPrefetch = func(owned context.Context, call securestorage.Command) (*securestorage.KeychainPrefetchProcess, error) {
				readsMu.Lock()
				reads = append(reads, call)
				readsMu.Unlock()
				securityCommand := exec.CommandContext(owned, filepath.Join(home, "private-empty-security.sh"), call.Args...)
				securityCommand.Dir, securityCommand.Env = home, []string{"HOME=" + home, "USERPROFILE=" + home, "PATH=/usr/bin:/bin", "TMPDIR=" + home}
				return securestorage.StartKeychainPrefetchProcess(owned, securityCommand)
			}
			code, actual, diagnostics, failure := mcpCLIExecute(t, n, args)
			if failure != nil || code != 0 || actual != stdout.String() || diagnostics != "" {
				t.Fatal("ordinary native MCP result", code, actual, diagnostics, failure)
			}
			gotKinds := map[string]int{}
			readsMu.Lock()
			defer readsMu.Unlock()
			for _, read := range reads {
				kind := "legacy"
				if strings.Contains(strings.Join(read.Args, " "), "-credentials") {
					kind = "oauth"
				}
				if read.ShellScript == "" {
					kind = "async-" + kind
				} else {
					kind = "sync-" + kind
				}
				gotKinds[kind]++
			}
			t.Logf("Source raw=%s Native raw=%+v Source classes=%v Native classes=%v", data, reads, wantKinds, gotKinds)
			// Counts come from the complete actual Source producer, including
			// advisor's API-key-first synchronous lookup. No expected total is
			// inferred from a token flag or the independent zero-read catalog.
			if !reflect.DeepEqual(gotKinds, wantKinds) || len(reads) != len(source.Prefetched)+len(source.SyncCalls) {
				t.Errorf("whole startup physical credential producers differ Source=%v (logicalReads%d getters%d) Native=%v; catalog remained zero", wantKinds, len(source.LogicalReads), source.StorageGetters, gotKinds)
			}
		})
	}
	if !reflect.DeepEqual(identity(), before) {
		t.Fatal("actual Source/caller input changed during count observation")
	}
}
