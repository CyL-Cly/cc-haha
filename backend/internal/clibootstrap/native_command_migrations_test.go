package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/plugincli"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// These are the physical outcomes of the whole Original main preAction
// profiles in native-command-migrations-source.test.ts. Leaf Run is a separate
// API: only the actual init caller acquires migration effects.
func TestNativeCommandPreActionRunsOriginalV11PhysicalMigrations(t *testing.T) {
	for _, kind := range []string{"sonnet-pro", "sonnet-max-from-1m", "opus-max", "opus-pro", "opus-unknown", "bypass-managed", "bypass-project-only", "mcp-git", "changelog-new", "changelog-existing", "changelog-write-failure", "already-v11"} {
		t.Run(kind, func(t *testing.T) {
			f := nativeCommandMigrationFixture(t, kind)
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodHead {
					t.Errorf("migration fixture sent non-HEAD request: %s", r.Method)
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer peer.Close()
			f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
			m, err := NewMainCommand(MainCommandOptions{Arguments: []string{"plugin", "marketplace", "list", "--json"}, Native: f.n, Platform: runtime.GOOS, PID: os.Getpid(), Version: "private-v11-migration", NonInteractive: true, Output: io.Discard, Error: io.Discard})
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			before := migrationReadObject(t, filepath.Join(f.config, ".claude.json"))
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if err := m.Run(ctx, func(*cooperative.Scope) error { return nil }); err != nil {
				t.Fatal(err)
			}
			afterLeaf := migrationReadObject(t, filepath.Join(f.config, ".claude.json"))
			if string(before.Get("migrationVersion")) != string(afterLeaf.Get("migrationVersion")) || afterLeaf.Get("firstStartTime") != nil {
				t.Fatal("generic leaf Run acquired CLI initialization")
			}
			if err := m.BeginNativeAPIPreconnect(ctx); err != nil {
				t.Fatal(err)
			}
			if err := m.Run(ctx, func(scope *cooperative.Scope) error {
				result, err := m.PluginCommand(ctx, scope, plugincli.Command{Action: "marketplace-list", JSON: true})
				if err == nil && (result.ExitCode != 0 || result.Stderr != "") {
					return fmt.Errorf("original action failed: %+v", result)
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			m.Close() // Join the original detached changelog/local-ignore jobs.
			global := migrationReadObject(t, filepath.Join(f.config, ".claude.json"))
			user := migrationReadObject(t, filepath.Join(f.config, "settings.json"))
			if string(global.Get("migrationVersion")) != "11" || global.String("firstStartTime") == "" {
				t.Fatalf("actual preAction omitted v11/first-start persistence: %s", mustMigrationJSON(global))
			}
			if global.Get("futureGlobal") == nil || user.Get("futurePrivate") == nil {
				t.Fatal("migration dropped unknown user/global fields")
			}
			switch kind {
			case "sonnet-pro":
				if user.String("model") != "sonnet" || global.Get("sonnet45To46MigrationTimestamp") == nil {
					t.Fatal("subscriber Sonnet migration did not reach physical settings", mustMigrationJSON(user), mustMigrationJSON(global))
				}
			case "sonnet-max-from-1m":
				if user.String("model") != "sonnet[1m]" || global.Get("sonnet45To46MigrationTimestamp") == nil {
					t.Fatal("ordered 1m then subscriber Sonnet migration lost its suffix", mustMigrationJSON(user))
				}
			case "opus-max":
				if user.Get("model") != nil {
					t.Fatal("default-equivalent Opus1m migration must delete the own model key", mustMigrationJSON(user))
				}
			case "opus-pro", "opus-unknown":
				if user.String("model") != "opus" {
					t.Fatal("ineligible subscriber acquired Opus1m", mustMigrationJSON(user))
				}
			case "bypass-managed", "bypass-project-only":
				if global.Get("bypassPermissionsModeAccepted") != nil || (user.Get("skipDangerousModePermissionPrompt") != nil) != (kind == "bypass-project-only") {
					t.Fatal("bypass migration did not consume the actual permitted source set", mustMigrationJSON(user), mustMigrationJSON(global))
				}
			case "mcp-git":
				local := migrationReadObject(t, filepath.Join(f.n.CWD, ".claude", "settings.local.json"))
				if string(local.Get("enableAllProjectMcpServers")) != "false" || string(protocol.StringifyValidatedJavaScriptJSON(local.Get("enabledMcpjsonServers"))) != `["local","shared","old"]` || string(protocol.StringifyValidatedJavaScriptJSON(local.Get("disabledMcpjsonServers"))) != `["local-disabled","disabled"]` || local.Get("futureLocal") == nil {
					t.Fatal("canonical Git MCP approval migration changed actual local data", mustMigrationJSON(local))
				}
				var projects protocol.JSONObject
				_ = projects.UnmarshalJSON(global.Get("projects"))
				var project protocol.JSONObject
				_ = project.UnmarshalJSON(projects.Get(filepath.ToSlash(f.cwd)))
				if project.Get("enableAllProjectMcpServers") != nil || project.Get("enabledMcpjsonServers") != nil || project.Get("disabledMcpjsonServers") != nil || project.Get("futureProject") == nil {
					t.Fatal("canonical Git project fields were not consumed", mustMigrationJSON(project))
				}
				ignore, err := os.ReadFile(filepath.Join(f.home, ".config", "git", "ignore"))
				if err != nil || !strings.Contains(string(ignore), "**/.claude/settings.local.json") {
					t.Fatal("real local-settings Git-ignore producer missing", err, string(ignore))
				}
			case "changelog-new", "changelog-existing", "changelog-write-failure":
				if global.Get("cachedChangelog") != nil {
					t.Fatal("detached changelog completion did not remove its original field")
				}
				path := filepath.Join(f.config, "cache", "changelog.md")
				if kind == "changelog-write-failure" {
					info, err := os.Stat(path)
					if err != nil || !info.IsDir() {
						t.Fatal("ordinary changelog write failure changed the existing directory", err)
					}
				} else {
					data, err := os.ReadFile(path)
					want := "# owned migration changelog\n"
					if kind == "changelog-existing" {
						want = "# already-owned cache\n"
					}
					if err != nil || string(data) != want {
						t.Fatal("original wx changelog semantics changed", err, string(data))
					}
				}
			case "already-v11":
				if global.Get("opusProMigrationComplete") != nil || global.Get("sonnet1m45MigrationComplete") != nil {
					t.Fatal("already-v11 reran synchronous migrations")
				}
			}
		})
	}
}

func nativeCommandMigrationFixture(t *testing.T, kind string) nativePluginFixture {
	t.Helper()
	f := newNativePluginFixture(t)
	f.n.CCRCredentialDirectory = filepath.Join(f.home, "remote-credentials")
	f.n.Environment["NODE_ENV"] = "production"
	f.n.Environment["USER_TYPE"] = "external"
	global := map[string]any{"numStartups": 3, "futureGlobal": map[string]bool{"keep": true}}
	user := map[string]any{"futurePrivate": map[string]bool{"keep": true}}
	model := ""
	if kind == "sonnet-pro" {
		model = "claude-sonnet-4-5-20250929"
	} else if kind == "sonnet-max-from-1m" {
		model = "sonnet[1m]"
	} else if strings.HasPrefix(kind, "opus-") {
		model = "opus"
	}
	if model != "" {
		user["model"] = model
		global["oauthAccount"] = map[string]any{"accountUuid": "owned-account", "organizationUuid": "owned-organization", "emailAddress": "owned@fixture.invalid", "billingType": "stripe_subscription", "accountCreatedAt": "2020-01-01T00:00:00.000Z", "subscriptionCreatedAt": "2020-01-01T00:00:00.000Z", "hasExtraUsageEnabled": false}
		subscription := any("max")
		if strings.HasSuffix(kind, "-pro") {
			subscription = "pro"
		} else if kind == "opus-unknown" {
			subscription = nil
		}
		credentials, _ := json.Marshal(map[string]any{"claudeAiOauth": map[string]any{"accessToken": "fake-owned-command-cached-token", "scopes": []string{"user:inference", "user:profile"}, "expiresAt": float64(time.Now().Add(365 * 24 * time.Hour).UnixMilli()), "subscriptionType": subscription, "rateLimitTier": "default_claude_max_5x"}})
		f.write(t, filepath.Join(f.config, ".credentials.json"), string(credentials))
	} else {
		const key = "fake-owned-command-1p-env-key"
		f.n.Environment["ANTHROPIC_API_KEY"] = key
		global["customApiKeyResponses"] = map[string]any{"approved": []string{key[len(key)-20:]}, "rejected": []string{}}
	}
	if strings.HasPrefix(kind, "bypass-") {
		global["bypassPermissionsModeAccepted"] = true
		if kind == "bypass-managed" {
			f.write(t, filepath.Join(f.managed, "managed-settings.json"), `{"skipDangerousModePermissionPrompt":true}`)
		} else {
			f.write(t, filepath.Join(f.cwd, ".claude", "settings.json"), `{"skipDangerousModePermissionPrompt":true,"futureProject":{"keep":true}}`)
		}
	}
	if kind == "mcp-git" {
		actualGit := "/usr/bin/git"
		if runtime.GOOS == "darwin" {
			actualGit = "/Library/Developer/CommandLineTools/usr/bin/git"
		}
		git := exec.Command(actualGit, "init", "--quiet", f.cwd)
		git.Env = []string{"HOME=" + f.home, "PATH=/usr/bin:/bin", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + filepath.Join(f.home, ".gitconfig")}
		if output, err := git.CombinedOutput(); err != nil {
			t.Fatal("private real Git prerequisite", err, string(output))
		}
		f.n.Environment["PATH"] = filepath.Dir(actualGit) + ":/usr/bin:/bin"
		f.n.Environment["GIT_CONFIG_NOSYSTEM"] = "1"
		f.n.Environment["GIT_CONFIG_GLOBAL"] = filepath.Join(f.home, ".gitconfig")
		f.n.CWD = filepath.Join(f.cwd, "nested")
		f.write(t, filepath.Join(f.n.CWD, ".claude", "settings.local.json"), `{"enableAllProjectMcpServers":false,"enabledMcpjsonServers":["local","shared"],"disabledMcpjsonServers":["local-disabled"],"futureLocal":{"keep":true}}`)
		global["projects"] = map[string]any{filepath.ToSlash(f.cwd): map[string]any{"enableAllProjectMcpServers": true, "enabledMcpjsonServers": []string{"old", "shared"}, "disabledMcpjsonServers": []string{"disabled"}, "futureProject": map[string]bool{"keep": true}}}
	}
	if strings.HasPrefix(kind, "changelog-") {
		global["cachedChangelog"] = "# owned migration changelog\n"
		if kind == "changelog-existing" {
			f.write(t, filepath.Join(f.config, "cache", "changelog.md"), "# already-owned cache\n")
		} else if kind == "changelog-write-failure" {
			if err := os.MkdirAll(filepath.Join(f.config, "cache", "changelog.md"), 0700); err != nil {
				t.Fatal(err)
			}
		}
	}
	if kind == "already-v11" {
		global["migrationVersion"] = 11
	}
	globalJSON, _ := json.Marshal(global)
	userJSON, _ := json.Marshal(user)
	f.write(t, filepath.Join(f.config, ".claude.json"), string(globalJSON))
	f.write(t, filepath.Join(f.config, "settings.json"), string(userJSON))
	return f
}

func migrationReadObject(t *testing.T, path string) protocol.JSONObject {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value protocol.JSONObject
	if err := value.UnmarshalJSON(data); err != nil {
		t.Fatal(err)
	}
	return value
}

func mustMigrationJSON(value protocol.JSONObject) string {
	raw, _ := value.MarshalJSON()
	return string(raw)
}

func TestNativeCommandMigrationSourceBindingRejectsForeignExpiredAndChangedHost(t *testing.T) {
	f := nativeCommandMigrationFixture(t, "already-v11")
	m, err := NewMainCommand(MainCommandOptions{Arguments: []string{"plugin", "list", "--json"}, Native: f.n, Platform: runtime.GOOS, PID: os.Getpid(), Version: "private-migration-guard", NonInteractive: true, Output: io.Discard, Error: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	var source *clistartupenv.NativeMigrationSources
	var expired *cooperative.Scope
	if err := m.Run(context.Background(), func(scope *cooperative.Scope) error {
		expired = scope
		var err error
		source, err = m.Startup.BindNativeMigrationSources(scope, m.Owner, m.Settings.settings, m.Settings.settingsState, m.Global)
		if err != nil {
			return err
		}
		if _, err := m.Startup.BindNativeMigrationSources(scope, m.Owner, m.Settings.settings, nil, m.Global); !errors.Is(err, cooperative.ErrRestricted) {
			return fmt.Errorf("missing source state accepted: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := source.Check(expired, m.Owner, m.Global); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("expired source continuation accepted", err)
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if err := foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		if _, err := source.Source(scope, "userSettings"); !errors.Is(err, cooperative.ErrRestricted) {
			return fmt.Errorf("foreign source owner accepted: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.Run(context.Background(), func(scope *cooperative.Scope) error {
		if err := m.Settings.environment.SetProperty(quoteMain("USER_TYPE"), quoteMain("ant")); err != nil {
			return err
		}
		_, err := source.Source(scope, "userSettings")
		var capability *clistartupenv.CapabilityError
		if !errors.As(err, &capability) {
			return fmt.Errorf("changed schema host accepted: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNativeMainPrintMigrationReachesMergedSettingsBeforeModelResolution(t *testing.T) {
	f := nativeCommandMigrationFixture(t, "sonnet-pro")
	disabled := false
	m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, SystemPrompt: "private v11 constructor consumer", Tools: []string{""}, MaxTurns: 1, Sources: DefaultState()}, Native: f.n, Platform: runtime.GOOS, PID: os.Getpid(), Version: "private-v11-main", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	user := migrationReadObject(t, filepath.Join(f.config, "settings.json"))
	global := migrationReadObject(t, filepath.Join(f.config, ".claude.json"))
	if user.String("model") != "sonnet" || string(global.Get("migrationVersion")) != "11" || global.String("firstStartTime") == "" {
		t.Fatal("ordinary model factory bypassed actual CLI migrations", mustMigrationJSON(user), mustMigrationJSON(global))
	}
	settings, err := m.Startup.GetInitialSettings()
	if err != nil || settings == nil || settings.String("model") != "sonnet" {
		t.Fatal("model settings cache retained pre-migration model", err, settings)
	}
	if strings.Contains(m.options.Arguments.Model, "4-5") || !strings.Contains(m.options.Arguments.Model, "sonnet") {
		t.Fatal("default model resolution preceded the migration", m.options.Arguments.Model)
	}
}
