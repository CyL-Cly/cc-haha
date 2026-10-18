package application

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestActualSourceExportFreshLogoBordersAndMarketNotice(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("pinned actual Source required")
	}
	type exportLogoProfile struct {
		name           string
		columns        int
		environment    map[string]string
		complete, home bool
		account        map[string]any
		settings       map[string]any
		arguments      []string
		global         map[string]any
		subscription   string
		ripgrep        bool
	}
	profiles := []exportLogoProfile{
		{name: "full", columns: 80},
		{name: "compact", columns: 42},
		{name: "wide", columns: 132},
		{name: "apple", columns: 80, environment: map[string]string{"TERM_PROGRAM": "Apple_Terminal"}},
		{name: "condensed", columns: 80, complete: true},
		{name: "home", columns: 80, home: true},
		{name: "organization-unicode", columns: 80, account: map[string]any{"displayName": "实际读者", "organizationName": "原始组织"}},
		{name: "demo", columns: 80, environment: map[string]string{"DEMO_VERSION": "demo-1", "IS_DEMO": "1"}},
		{name: "announcement-full", columns: 80, account: map[string]any{"organizationName": "真实团队"}, settings: map[string]any{"companyAnnouncements": []string{"原始团队消息包含真实业务信息"}}},
		{name: "announcement-condensed", columns: 80, complete: true, settings: map[string]any{"companyAnnouncements": []string{"原始团队消息包含真实业务信息"}}},
		{name: "announcement-compact-negative", columns: 42, settings: map[string]any{"companyAnnouncements": []string{"原始团队消息包含真实业务信息"}}},
		{name: "tmux", columns: 80, environment: map[string]string{"CLAUDE_CODE_TMUX_SESSION": "actual-private-tmux", "CLAUDE_CODE_TMUX_PREFIX": "Ctrl+B"}},
		{name: "tmux-conflict-condensed", columns: 80, complete: true, environment: map[string]string{"CLAUDE_CODE_TMUX_SESSION": "actual-private-tmux", "CLAUDE_CODE_TMUX_PREFIX": "Ctrl+B", "CLAUDE_CODE_TMUX_PREFIX_CONFLICTS": "1"}},
		{name: "debug-log", columns: 80, environment: map[string]string{"DEBUG": "1"}},
		{name: "debug-stderr", columns: 80, complete: true, arguments: []string{"--debug-to-stderr"}},
		{name: "guest-passes-full", columns: 80, complete: true, subscription: "max", account: map[string]any{"organizationUuid": "private-export-org"}, environment: map[string]string{"CLAUDE_CODE_FORCE_FULL_LOGO": "1"}, global: map[string]any{"passesEligibilityCache": map[string]any{"private-export-org": map[string]any{"eligible": true, "timestamp": 9999999999999, "remaining_passes": 2, "referrer_reward": map[string]any{"amount_minor_units": 1250, "currency": "EUR"}}}, "overageCreditGrantCache": map[string]any{"private-export-org": map[string]any{"timestamp": 9999999999999, "info": map[string]any{"available": false}}}}},
		{name: "overage-credit-full", columns: 80, complete: true, subscription: "pro", account: map[string]any{"organizationUuid": "private-export-org"}, environment: map[string]string{"CLAUDE_CODE_FORCE_FULL_LOGO": "1"}, global: map[string]any{"overageCreditGrantCache": map[string]any{"private-export-org": map[string]any{"timestamp": 9999999999999, "info": map[string]any{"available": true, "eligible": true, "granted": false, "amount_minor_units": 2025, "currency": "usd"}}}}},
		{name: "external-feature-cache-negative", columns: 80, global: map[string]any{"cachedGrowthBookFeatures": map[string]any{"tengu-top-of-feed-tip": map[string]any{"tip": "must never bypass empty external client key"}}, "cachedDynamicConfigs": map[string]any{"tengu-top-of-feed-tip": map[string]any{"tip": "must never bypass empty external client key"}}}},
	}
	for _, variant := range []string{"guest-reset", "guest-cap-negative", "guest-stale-cache", "guest-compact", "guest-condensed", "overage-cap-negative", "overage-granted-negative", "overage-currency-negative", "overage-expired-negative", "overage-condensed"} {
		base := profiles[15]
		if strings.HasPrefix(variant, "overage") {
			base = profiles[16]
		}
		base.name = variant
		var globalCopy map[string]any
		var environmentCopy map[string]string
		_ = json.Unmarshal(raw(base.global), &globalCopy)
		_ = json.Unmarshal(raw(base.environment), &environmentCopy)
		base.global, base.environment = globalCopy, environmentCopy
		switch variant {
		case "guest-reset":
			base.global["passesUpsellSeenCount"], base.global["hasVisitedPasses"], base.global["passesLastSeenRemaining"] = 3, true, 1
		case "guest-cap-negative":
			base.global["passesUpsellSeenCount"], base.global["hasVisitedPasses"], base.global["passesLastSeenRemaining"] = 3, true, 2
		case "guest-stale-cache":
			base.global["passesEligibilityCache"].(map[string]any)["private-export-org"].(map[string]any)["timestamp"] = 0
		case "guest-compact":
			base.columns = 42
		case "overage-cap-negative":
			base.global["overageCreditUpsellSeenCount"] = 3
		case "overage-granted-negative":
			base.global["overageCreditGrantCache"].(map[string]any)["private-export-org"].(map[string]any)["info"].(map[string]any)["granted"] = true
		case "overage-currency-negative":
			base.global["overageCreditGrantCache"].(map[string]any)["private-export-org"].(map[string]any)["info"].(map[string]any)["currency"] = "EUR"
		case "overage-expired-negative":
			base.global["overageCreditGrantCache"].(map[string]any)["private-export-org"].(map[string]any)["timestamp"] = 0
		case "overage-condensed", "guest-condensed":
			delete(base.environment, "CLAUDE_CODE_FORCE_FULL_LOGO")
		}
		profiles = append(profiles, base)
	}
	profiles = append(profiles,
		exportLogoProfile{name: "sandbox-full", columns: 80, ripgrep: true, settings: map[string]any{"sandbox": map[string]any{"enabled": true}}},
		exportLogoProfile{name: "sandbox-compact", columns: 42, ripgrep: true, settings: map[string]any{"sandbox": map[string]any{"enabled": true}}},
		exportLogoProfile{name: "sandbox-condensed", columns: 80, complete: true, ripgrep: true, settings: map[string]any{"sandbox": map[string]any{"enabled": true}}},
		exportLogoProfile{name: "sandbox-missing-rg-negative", columns: 80, settings: map[string]any{"sandbox": map[string]any{"enabled": true}}},
	)
	for _, profile := range profiles {
		t.Run(profile.name, func(t *testing.T) {
			o := nativeSessionOptions(t, "http://127.0.0.1:9")
			o.Interactive = true
			o.Arguments.DebugArguments = append([]string(nil), profile.arguments...)
			if profile.subscription != "" {
				delete(o.Environment, "ANTHROPIC_API_KEY")
			}
			for key, value := range profile.environment {
				o.Environment[key] = value
			}
			home := o.Environment["HOME"]
			if profile.ripgrep {
				// Copy the actual executable into this disposable home. Both
				// profiles use Source's existing explicit packaged-rg resolver;
				// neither the enabled predicate nor dependency check is mocked.
				original, err := exec.LookPath("rg")
				if err != nil {
					t.Fatal("real packaged ripgrep required", err)
				}
				binary, err := os.ReadFile(original)
				if err != nil {
					t.Fatal(err)
				}
				private := filepath.Join(home, "bin", "rg")
				if err = os.MkdirAll(filepath.Dir(private), 0700); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(private, binary, 0700); err != nil {
					t.Fatal(err)
				}
				o.Environment["CC_HAHA_RIPGREP_PATH"] = private
				o.Environment["CLAUDE_CODE_TMPDIR"] = filepath.Join(home, "tmp")
				if err = os.MkdirAll(o.Environment["CLAUDE_CODE_TMPDIR"], 0700); err != nil {
					t.Fatal(err)
				}
			}
			work := filepath.Join(home, "work")
			if profile.home {
				work = home
			}
			for _, path := range []string{work, filepath.Join(home, "managed"), o.Environment["CLAUDE_CONFIG_DIR"]} {
				if err := os.MkdirAll(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			n, err := clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: o.Environment, CWD: work, Platform: "darwin", ManagedRoot: filepath.Join(home, "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
			if err != nil {
				t.Fatal(err)
			}
			n.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrNotExist }
			o.Native, o.WorkDir = n, n.CWD
			global := map[string]any{"future": map[string]any{"preserve": 17}}
			for key, value := range profile.global {
				global[key] = value
			}
			if profile.complete {
				global["projects"] = map[string]any{o.WorkDir: map[string]any{"hasCompletedProjectOnboarding": true}}
			}
			if profile.account != nil {
				global["oauthAccount"] = profile.account
			}
			if err = os.WriteFile(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], ".claude.json"), raw(global), 0600); err != nil {
				t.Fatal(err)
			}
			if profile.settings != nil {
				if err = os.WriteFile(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), raw(profile.settings), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if profile.subscription != "" {
				credentials := map[string]any{"claudeAiOauth": map[string]any{"accessToken": "fake-private-export-token", "refreshToken": nil, "scopes": []string{"user:inference", "user:profile"}, "expiresAt": 9999999999999, "subscriptionType": profile.subscription}}
				if err = os.WriteFile(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], ".credentials.json"), raw(credentials), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err = os.WriteFile(filepath.Join(work, "actual-project.txt"), []byte("not empty"), 0600); err != nil {
				t.Fatal(err)
			}
			session, err := NewSession(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close(context.Background())
			if profile.ripgrep {
				port, socks, err := session.process.NativeSandboxProxyPorts(context.Background())
				if err != nil || port <= 0 || socks <= 0 {
					t.Fatal("actual sandbox initialization", port, socks, err)
				}
			}
			value, err := session.process.TerminalConversation(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			presentation, err := session.process.TerminalExportPresentation(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			physicalColumns := presentation.Columns
			presentation.Columns = profile.columns
			beforeSourceConfig, err := os.ReadFile(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], ".claude.json"))
			if err != nil {
				t.Fatal(err)
			}
			// Source mounts real effects. Its private writes must not enter the
			// live Go global-config watcher before the independent Go consumer.
			sourceHome, sourceWork := home, o.WorkDir
			if profile.subscription != "" {
				sourceHome = t.TempDir()
				relativeWork, err := filepath.Rel(home, o.WorkDir)
				if err != nil || relativeWork == ".." || strings.HasPrefix(relativeWork, ".."+string(os.PathSeparator)) {
					t.Fatal("Source renderer work must remain in its disposable home", relativeWork, err)
				}
				sourceWork = filepath.Join(sourceHome, relativeWork)
				if err = cloneTerminalExportRendererHome(home, sourceHome); err != nil {
					t.Fatal(err)
				}
				var sourceGlobal map[string]json.RawMessage
				if err = json.Unmarshal(beforeSourceConfig, &sourceGlobal); err != nil {
					t.Fatal(err)
				}
				if projectsRaw := sourceGlobal["projects"]; len(projectsRaw) > 0 {
					var projects map[string]json.RawMessage
					if err = json.Unmarshal(projectsRaw, &projects); err != nil {
						t.Fatal(err)
					}
					if project, ok := projects[o.WorkDir]; ok {
						delete(projects, o.WorkDir)
						projects[sourceWork] = project
						sourceGlobal["projects"] = raw(projects)
					}
				}
				if err = os.WriteFile(filepath.Join(sourceHome, ".claude", ".claude.json"), raw(sourceGlobal), 0600); err != nil {
					t.Fatal(err)
				}
			}
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-export-renderer.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			command.Dir = sourceHome
			command.Env = []string{"HOME=" + sourceHome, "USERPROFILE=" + sourceHome, "CLAUDE_CONFIG_DIR=" + filepath.Join(sourceHome, ".claude"), "TMPDIR=" + sourceHome, "PATH=/usr/bin:/bin", "TZ=UTC", "USER_TYPE=external", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(sourceHome), "CC_HAHA_TERMINAL_EXPORT_FIXTURE=1"}
			for key, value := range profile.environment {
				command.Env = append(command.Env, key+"="+value)
			}
			for _, key := range []string{"CC_HAHA_RIPGREP_PATH", "CLAUDE_CODE_TMPDIR"} {
				if value := o.Environment[key]; value != "" {
					command.Env = append(command.Env, key+"="+value)
				}
			}
			command.Stdin = bytes.NewReader(raw(map[string]any{"home": sourceHome, "cwd": sourceWork, "version": o.Version, "sid": session.process.Controller.SessionID(), "arguments": profile.arguments, "cachedSubscriber": profile.subscription != "", "preload": true, "captureLayout": profile.name == "guest-condensed", "rows": []any{map[string]any{"messages": value.Messages, "columns": profile.columns}}}))
			var stderr bytes.Buffer
			command.Stderr = &stderr
			output, err := command.Output()
			if profile.name == "guest-condensed" {
				_ = os.WriteFile(filepath.Join(os.Getenv("TMPDIR"), "session-export-guest-layout.json"), output, 0600)
			}
			if err != nil {
				t.Fatal(err, string(output), stderr.String())
			}
			var source struct {
				Results      []string
				GlobalConfig protocol.JSONObject
				FeatureGates struct{ Voice, Channels bool }
				Caps         struct{ Blocked []string }
				Sandboxed    bool
			}
			if err = json.Unmarshal(output, &source); err != nil || len(source.Results) != 1 || len(source.Caps.Blocked) != 0 {
				t.Fatal(err, string(output))
			}
			got := tui.RenderConversationText(value.Messages, presentation)
			if strings.TrimRight(got, "\n") != strings.TrimRight(source.Results[0], "\n") {
				t.Fatalf("actual fresh React export differs\nGo:\n%s\nSource:\n%s", got, source.Results[0])
			}
			if source.FeatureGates.Voice || source.FeatureGates.Channels {
				t.Fatal("frozen external Source feature profile changed", source.FeatureGates)
			}
			if source.Sandboxed != presentation.Sandboxed || profile.ripgrep && !presentation.Sandboxed {
				t.Fatal("actual sandbox notice availability differs", presentation.Sandboxed, source.Sandboxed)
			}
			if profile.ripgrep && profile.columns == physicalColumns {
				if handled, err := session.InvokeTerminalCommand(context.Background(), "export", "actual-sandbox-business.txt", nil); err != nil || !handled {
					t.Fatal("actual sandbox export file consumer", handled, err)
				}
				file, err := os.ReadFile(filepath.Join(o.WorkDir, "actual-sandbox-business.txt"))
				if err != nil || string(file) != got {
					t.Fatal("actual sandbox export omitted original rendered business", err, string(file), got)
				}
			}
			if profile.subscription != "" {
				// Each actual consumer starts from the same private persisted
				// state. Source's mounted effects must not count as an earlier
				// invocation of the Go product entry.
				if err = os.WriteFile(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], ".claude.json"), beforeSourceConfig, 0600); err != nil {
					t.Fatal(err)
				}
				if profile.columns == physicalColumns {
					if handled, err := session.InvokeTerminalCommand(context.Background(), "export", "actual-cached-business.txt", nil); err != nil || !handled {
						t.Fatal("actual cached business file consumer", handled, err)
					}
					file, err := os.ReadFile(filepath.Join(o.WorkDir, "actual-cached-business.txt"))
					if err != nil || string(file) != got {
						t.Fatal("actual export omitted cached business content", err, string(file), got)
					}
				} else if err = session.process.CommitTerminalExportRenderEffects(context.Background(), presentation); err != nil {
					// The pipe-backed public process is 80 columns. Source's
					// compact profile proves renderer/effect semantics here;
					// real terminal dimensions are verified by the PTY consumer.
					t.Fatal("compact mounted effects", err)
				}
				configRaw, err := os.ReadFile(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], ".claude.json"))
				var actual protocol.JSONObject
				if err != nil || actual.UnmarshalJSON(configRaw) != nil {
					t.Fatal(err, string(configRaw))
				}
				for _, key := range []string{"passesUpsellSeenCount", "hasVisitedPasses", "passesLastSeenRemaining", "overageCreditUpsellSeenCount", "future"} {
					got, want := actual.Get(key), source.GlobalConfig.Get(key)
					if len(got) > 0 {
						got = protocol.StringifyValidatedJavaScriptJSON(got)
					}
					if len(want) > 0 {
						want = protocol.StringifyValidatedJavaScriptJSON(want)
					}
					if !bytes.Equal(got, want) {
						t.Fatal("mounted original business effect differs", key, string(actual.Get(key)), string(source.GlobalConfig.Get(key)))
					}
				}
			}
		})
	}
}

// Copy only the test's existing disposable HOME, preserving file modes and
// rejecting symlinks or other special files rather than following them.
func cloneTerminalExportRendererHome(from, to string) error {
	return filepath.WalkDir(from, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		target := filepath.Join(to, relative)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return &os.PathError{Op: "clone disposable renderer fixture", Path: path, Err: os.ErrInvalid}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
}
