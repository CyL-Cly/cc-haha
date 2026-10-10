package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestActualSDKPermissionDiskFailureKeepsLiveApprovalAndOriginalFile(t *testing.T) {
	m := privateWebMain(t, "http://127.0.0.1:9", nil)
	var logs bytes.Buffer
	m.options.Error = &logs
	path := filepath.Join(m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "settings.json")
	broken := []byte("{invalid old settings")
	if e := os.WriteFile(path, broken, 0600); e != nil {
		t.Fatal(e)
	}
	updates := json.RawMessage(`[{"type":"addRules","destination":"userSettings","behavior":"allow","rules":[{"toolName":"WebFetch","ruleContent":"domain:allowed.example"}]}]`)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		if e := m.Host.ApplyNativePermissionUpdates(scope, updates); e != nil {
			return e
		}
		actual, e := m.Host.NativePermissionContext(scope)
		if e != nil {
			return e
		}
		var allowed protocol.JSONObject
		_ = allowed.UnmarshalJSON(actual.Get("alwaysAllowRules"))
		if !strings.Contains(string(allowed.Get("userSettings")), "WebFetch(domain:allowed.example)") {
			t.Errorf("live approval revoked%s", actual.Get("alwaysAllowRules"))
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	actual, e := os.ReadFile(path)
	if e != nil || !bytes.Equal(actual, broken) {
		t.Fatalf("broken original overwritten%q %v", actual, e)
	}
	if !strings.Contains(logs.String(), "Invalid JSON syntax in settings file") {
		t.Fatalf("missing actual persistence diagnostic %q", logs.String())
	}
}

func TestActualSDKPermissionAlwaysWritesLocalAndGlobalIgnoreAndSurvivesReload(t *testing.T) {
	m := privateWebMain(t, "http://127.0.0.1:9", nil)
	n := m.options.Native
	n.Environment["GIT_CONFIG_NOSYSTEM"] = "1"
	n.Environment["GIT_CONFIG_GLOBAL"] = filepath.Join(n.Home, "private.gitconfig")
	n.Environment["GIT_CONFIG_SYSTEM"] = "/dev/null"
	command := exec.Command("/usr/bin/git", "init", "--quiet")
	command.Dir = n.CWD
	command.Env = nativeAgentProcessEnvironment(n)
	if data, e := command.CombinedOutput(); e != nil {
		t.Fatalf("private Git %v %s", e, data)
	}
	updates := json.RawMessage(`[{"type":"addRules","destination":"localSettings","behavior":"allow","rules":[{"toolName":"WebFetch","ruleContent":"domain:example.org"}]}]`)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error { return m.Host.ApplyNativePermissionUpdates(scope, updates) }); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(n.CWD, ".claude", "settings.local.json")
	data, e := os.ReadFile(path)
	if e != nil || !strings.Contains(string(data), "WebFetch(domain:example.org)") {
		t.Fatalf("actual local settings %s %v", data, e)
	}
	if _, e := m.ReloadComponents(ctx); e != nil {
		t.Fatal(e)
	}
	if e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		current, e := m.Host.NativePermissionContext(scope)
		if e != nil {
			return e
		}
		var sources protocol.JSONObject
		_ = sources.UnmarshalJSON(current.Get("alwaysAllowRules"))
		if !strings.Contains(string(sources.Get("localSettings")), "WebFetch(domain:example.org)") {
			t.Errorf("rule missing after actual reload%s", sources.Get("localSettings"))
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if e := m.ShutdownSession(ctx); e != nil {
		t.Fatal(e)
	}
	ignore := filepath.Join(n.Home, ".config", "git", "ignore")
	data, e = os.ReadFile(ignore)
	if e != nil || string(data) != "**/.claude/settings.local.json\n" {
		t.Fatalf("real tracked global-ignore %s %v", data, e)
	}
}
