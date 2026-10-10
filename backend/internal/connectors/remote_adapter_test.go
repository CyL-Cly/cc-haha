package connectors

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoteAPIKeyLifecycleAgainstActualSource(t *testing.T) {
	home, row := remoteFixture(t, "api-key")
	other := filepath.Join(row.Root, "remote", "another", "keep")
	if err := os.MkdirAll(filepath.Dir(other), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	row.Operations = []remoteStep{{Action: "prepare"}, {Action: "check"}, {Action: "authenticate"}, {Action: "configure", Configuration: map[string]string{"apiKey": " secret&other=value#fragment!'() "}}, {Action: "check"}, {Action: "configure", Configuration: map[string]string{"apiKey": " "}}, {Action: "deactivate"}, {Action: "remove"}}
	out, _ := runRemoteScenario(t, home, row)
	if out.Observations[1].Error != nil || out.Observations[2].Error == nil || out.Observations[4].Error != nil {
		t.Fatalf("API key lifecycle %+v", out.Observations)
	}
	if value, err := os.ReadFile(other); err != nil || string(value) != "keep" {
		t.Fatalf("foreign connector removed %q %v", value, err)
	}
	if _, err := os.Stat(row.Installation.Directory); !os.IsNotExist(err) {
		t.Fatalf("own marker was not removed %v", err)
	}
	if len(out.Options) != 1 || out.Options["another-plugin@other"].String("apiKey") != "keep-other-secret" {
		t.Fatal("foreign plugin options changed")
	}
}
func TestRemoteHeaderCredentialResolutionValidationAndRotation(t *testing.T) {
	home, row := remoteFixture(t, "api-key")
	row.Recipe.Auth.In = "header"
	row.Recipe.Auth.Name = "Authorization"
	row.Recipe.Auth.Prefix = "Bearer ${PREFIX:-prefix:-truncated} ${CLAUDE_PLUGIN_ROOT} "
	row.Environment["PREFIX"] = "selected"
	row.Options[row.Recipe.PluginID] = record(field("apiKey", "old-key"))
	row.Operations = []remoteStep{{Action: "configure", Configuration: map[string]string{"apiKey": "new-key"}}, {Action: "bridge-check"}, {Action: "configure", Configuration: map[string]string{"endpoint": "https://evil.example"}}, {Action: "configure", Configuration: map[string]string{"apiKey": "key\r\ninjected"}}, {Action: "configure", Configuration: map[string]string{"apiKey": "${SECRET}"}}, {Action: "configure", Configuration: map[string]string{"apiKey": strings.Repeat("😀", 4097)}}, {Action: "configure", Configuration: map[string]string{"apiKey": strings.Repeat("😀", 4096)}}, {Action: "check"}}
	out, _ := runRemoteScenario(t, home, row)
	if out.Calls[0][0] != "disconnect" || out.Calls[1][0] != "save" {
		t.Fatalf("credentials saved before old connection detached: %+v", out.Calls)
	}
	for _, index := range []int{2, 3, 4, 5} {
		if out.Observations[index].Error == nil {
			t.Fatalf("invalid configuration accepted %d", index)
		}
	}
}
func TestRemoteOAuthProgressURLValidationClearSlotsAndFailures(t *testing.T) {
	for _, candidate := range []string{"https://accounts.example/authorize?state=fixture", "http://accounts.example/authorize", "https://user:password@accounts.example/authorize", "not a URL"} {
		t.Run(candidate, func(t *testing.T) {
			home, row := remoteFixture(t, "oauth")
			row.Operations = []remoteStep{{Action: "prepare"}, {Action: "authenticate", Behavior: record(field("authUrl", candidate))}, {Action: "bridge-check", Behavior: record(field("status", "needs-auth"))}, {Action: "bridge-check", Behavior: record(field("status", "connected"), field("toolCount", 0))}, {Action: "bridge-check", Behavior: record(field("fail", "probe"))}, {Action: "bridge-authenticate", Behavior: record(field("fail", "oauth"))}, {Action: "configure", Configuration: map[string]string{"apiKey": "wrong"}}, {Action: "remove", Behavior: record(field("fail", ""))}}
			out, _ := runRemoteScenario(t, home, row)
			if len(out.Observations[1].Phases) < 1 || out.Observations[1].Phases[0][0] != "authorizing" {
				t.Fatal("OAuth progress missing")
			}
			for _, i := range []int{3, 4, 5, 6} {
				if out.Observations[i].Error == nil {
					t.Fatalf("unverified operation succeeded %d", i)
				}
			}
			for _, op := range out.Observations {
				if op.Error != nil && strings.Contains(*op.Error, "sensitive-detail") {
					t.Fatal("raw probe/OAuth error exposed")
				}
			}
		})
	}
}
func TestRemoteOwnedInstallationRejectsMutationBeforeSideEffects(t *testing.T) {
	home, row := remoteFixture(t, "none")
	foreign := row.Installation
	foreign.Directory = filepath.Join(home, "foreign")
	injected := row.Installation
	injected.Command = "shell"
	injectedEnv := row.Installation
	injectedEnv.Environment = map[string]string{"USER_TOKEN": "fake"}
	row.Operations = []remoteStep{{Action: "check", Installation: &foreign}, {Action: "authenticate", Installation: &injected}, {Action: "remove", Installation: &injectedEnv}, {Action: "bridge-check", Cancelled: true}, {Action: "bridge-authenticate", Cancelled: true}, {Action: "config"}}
	out, _ := runRemoteScenario(t, home, row)
	if len(out.Calls) != 0 {
		t.Fatalf("foreign/cancelled installation produced runtime effects %+v", out.Calls)
	}
	for _, i := range []int{0, 1, 2, 3} {
		if out.Observations[i].Error == nil {
			t.Fatal("invalid installation or cancellation accepted")
		}
	}
}
func TestRemoteNativeBindingRequiresActualDependencies(t *testing.T) {
	_, row := remoteFixture(t, "none")
	if _, err := NewRemoteBridge(row.Recipe, RemoteDependencies{}); err == nil {
		t.Fatal("remote bridge fabricated MCP/OAuth/options capabilities")
	}
	if _, err := RemoteAdapter(record(field("id", "different")), row.Root, row.Recipe, RemoteDependencies{}); err == nil {
		t.Fatal("foreign definition admitted")
	}
}

func TestRemoteConfigVariablesAndPrimitiveFailuresAgainstActualSource(t *testing.T) {
	for _, prefix := range []string{"Bearer ${CLAUDE_PLUGIN_DATA} ", "Bearer ${UNSET:-default:-discarded} ", "Bearer ${UNSET} ", "Bearer ${user_config.other} ", "Bearer ${user_config.missing} "} {
		t.Run(prefix, func(t *testing.T) {
			home, row := remoteFixture(t, "api-key")
			row.Recipe.Auth.In = "header"
			row.Recipe.Auth.Name = "Authorization"
			row.Recipe.Auth.Prefix = prefix
			row.Options[row.Recipe.PluginID] = record(field("apiKey", "fake-key"), field("other", []string{"array", "coercion"}))
			row.Operations = []remoteStep{{Action: "bridge-check"}, {Action: "deactivate"}, {Action: "remove"}}
			_, _ = runRemoteScenario(t, home, row)
		})
	}
	home, row := remoteFixture(t, "none")
	row.Operations = []remoteStep{{Action: "prepare", Cancelled: true}, {Action: "authenticate", Cancelled: true}, {Action: "check", Cancelled: true}, {Action: "bridge-check", Behavior: record(field("status", "failed"))}, {Action: "bridge-check", Behavior: record(field("status", "disabled"))}, {Action: "bridge-check", Behavior: record(field("fail", "disconnect"))}, {Action: "deactivate"}, {Action: "remove"}}
	_, _ = runRemoteScenario(t, home, row)
}
