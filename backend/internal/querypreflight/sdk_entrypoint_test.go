package querypreflight

import (
	"context"
	"reflect"
	"testing"
)

func TestSDKOriginalBillingEnvironmentDoesNotChangePolicy(t *testing.T) {
	service, _, _, _, _ := testServices(t)
	original := base(t)
	a, e := service.Run(context.Background(), original)
	if e != nil {
		t.Fatal(e)
	}
	original.OriginalEnvironment["CLAUDE_CODE_ENTRYPOINT"] = "sdk"
	b, e := service.Run(context.Background(), original)
	if e != nil {
		t.Fatal("original SDK primitive cannot reach policy", e)
	}
	if a.CurrentModel != b.CurrentModel || !reflect.DeepEqual(a.SystemPrompt, b.SystemPrompt) || !reflect.DeepEqual(stringsOf(t, a.ModelHistory), stringsOf(t, b.ModelHistory)) || a.Blocked != nil || b.Blocked != nil {
		t.Fatal("billing field changed model policy")
	}
	if original.OriginalEnvironment["CLAUDE_CODE_ENTRYPOINT"] != "sdk" {
		t.Fatal("original host field removed")
	}
	for _, value := range []string{"", "cli", "unexpected"} {
		original.OriginalEnvironment["CLAUDE_CODE_ENTRYPOINT"] = value
		_, e := service.Run(context.Background(), original)
		requireCap(t, e)
	}
	original.OriginalEnvironment["CLAUDE_CODE_ENTRYPOINT"] = "sdk"
	original.OriginalEnvironment["UNKNOWN_ACTIVE_FLAG"] = "1"
	_, e = service.Run(context.Background(), original)
	requireCap(t, e)
}

func TestNativeCLIEntrypointKeepsOriginalPreflightPolicy(t *testing.T) {
	service, _, _, _, _ := testServices(t)
	in := base(t)
	in.Profile = NativeProcessProfile
	in.OriginalEnvironment["CLAUDE_CODE_ENTRYPOINT"] = "sdk-cli"
	a, err := service.Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	in.OriginalEnvironment["CLAUDE_CODE_ENTRYPOINT"] = "cli"
	b, err := service.Run(context.Background(), in)
	if err != nil {
		t.Fatal("ordinary native TUI cannot reach preflight", err)
	}
	if !reflect.DeepEqual(a, b) || in.OriginalEnvironment["CLAUDE_CODE_ENTRYPOINT"] != "cli" {
		t.Fatal("CLI identity changed native preflight policy", a, b)
	}
	for _, entry := range []string{"unexpected", "claude-desktop", "remote"} {
		in.OriginalEnvironment["CLAUDE_CODE_ENTRYPOINT"] = entry
		_, err = service.Run(context.Background(), in)
		requireCap(t, err)
	}
}
