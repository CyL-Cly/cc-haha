package sessioncontroller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/resumestate"
)

func TestOwnedSkillRawIdentityPreservesOriginalCompactionAndOldNormalRoundtrip(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Bun required")
	}
	base, _, owner := boundController(t)
	clock := int64(9000)
	opts := base.options
	opts.ResumeOptions.Now = func() int64 { return clock }
	opts.ResumeOptions.NativeSkillBindings = func() bool { return true }
	c, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.AwaitBackground)
	history := json.RawMessage(`[{"type":"attachment","uuid":"11111111-1111-4111-8111-111111111111","attachment":{"type":"invoked_skills","skills":[{"name":"old-normal","path":"bundled:old-normal","content":"OLD_NORMAL_BODY"},{"name":"old-second","path":"project:old-second","content":"OLD_SECOND_BODY"}]}}]`)
	type invocation struct {
		Name, Path     json.RawMessage
		Content, Agent string
		Now            float64
		Scalar         bool
	}
	calls := []invocation{
		{json.RawMessage(`"same-\ud800:reader"`), json.RawMessage(`"plugin:same-\ud800:reader"`), "HIGH_BODY", "", 10000, false},
		{json.RawMessage(`"same-\udc00:reader"`), json.RawMessage(`"plugin:same-\udc00:reader"`), "LOW_BODY", "", 11000, false},
		{json.RawMessage(`"same-\ud800:reader"`), json.RawMessage(`"plugin:same-\ud800:reader"`), "HIGH_UPDATED_BODY", "", 12000, false},
		{json.RawMessage(`"same-\ud800:reader"`), json.RawMessage(`"plugin:same-\ud800:reader"`), "CHILD_BODY", "child-actual", 13000, false},
		{json.RawMessage(`"old-normal"`), json.RawMessage(`"bundled:old-normal"`), "NORMAL_UPDATED_BODY", "", 14000, true},
	}
	got := []json.RawMessage{}
	if err = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		input := activation()
		input.Resume = resumestate.Input{Source: skillIdentityObjectSource(history)}
		if err := c.ActivateOwned(scope, input); err != nil {
			return err
		}
		got = append(got, skillIdentityEncode(c.resume.State().Skills))
		for _, call := range calls {
			clock = int64(call.Now)
			if call.Scalar {
				if err := c.InvokeSkill(scope, "old-normal", "bundled:old-normal", call.Content, call.Agent); err != nil {
					return err
				}
			} else if err := c.InvokeSkillJSON(scope, call.Name, call.Path, call.Content, call.Agent); err != nil {
				return err
			}
			got = append(got, skillIdentityEncode(c.resume.State().Skills))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if err = foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		return c.InvokeSkillJSON(scope, calls[0].Name, calls[0].Path, "forbidden", "")
	}); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("foreign raw producer admitted", err)
	}
	home := t.TempDir()
	cwd := filepath.Join(home, "project")
	temp := filepath.Join(home, "tmp")
	config := filepath.Join(home, ".claude")
	for _, dir := range []string{cwd, temp, config, filepath.Join(home, ".git"), filepath.Join(cwd, ".git")} {
		if err = os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, dir := range []string{home, cwd} {
		if err = os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref: refs/heads/private\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	fixture, _ := filepath.Abs("../clibootstrap/testdata/skill-lone-identity-source.fixture.ts")
	sandbox, _ := filepath.Abs("../../testdata/loopback-only.sb")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, bun, "--no-env-file", fixture)
	child.Dir = home
	child.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + temp, "PATH=/usr/bin:/bin", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_GUIDE_UTF16_FIXTURE=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_ENTRYPOINT=sdk-cli", "BUN_OPTIONS=--no-env-file"}
	child.Stdin = bytes.NewReader(skillIdentityEncode(map[string]any{"home": home, "cwd": cwd, "directories": []string{}, "compactionOnly": true, "history": history, "calls": calls}))
	var stderr bytes.Buffer
	child.Stderr = &stderr
	data, err := child.Output()
	if err != nil {
		t.Fatal("Original compaction/restore consumer", err, stderr.String())
	}
	var source struct {
		Snapshots    []json.RawMessage
		Attachment   protocol.JSONObject
		Restored     json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(data, &source); err != nil || len(source.Snapshots) != len(got) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal("Source observation missing", err, string(data))
	}
	for i, want := range source.Snapshots {
		if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(got[i]), protocol.StringifyValidatedJavaScriptJSON(want)) {
			t.Errorf("invocation%d Go%s Source%s", i, got[i], want)
		}
	}
	clock = 20000
	opts.ResumeOptions.CurrentSessionID = c.SessionID()
	opts.WriterOptions.SessionID = c.SessionID()
	resumed, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(resumed.AwaitBackground)
	if err = owner.Run(ctx, func(scope *cooperative.Scope) error {
		input := activation()
		input.Resume = resumestate.Input{Source: skillIdentityObjectSource(skillIdentityEncode([]protocol.JSONObject{source.Attachment}))}
		return resumed.ActivateOwned(scope, input)
	}); err != nil {
		t.Fatal("actual Source compaction attachment through controller resume", err)
	}
	actual := skillIdentityEncode(resumed.resume.State().Skills)
	if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(actual), protocol.StringifyValidatedJavaScriptJSON(source.Restored)) {
		t.Fatalf("Original normal/lone compaction roundtrip changed Go%s Source%s", actual, source.Restored)
	}
}
func skillIdentityEncode(value any) json.RawMessage { raw, _ := json.Marshal(value); return raw }
func skillIdentityObjectSource(history json.RawMessage) json.RawMessage {
	return skillIdentityEncode(protocol.NewJSONObject(protocol.JSONField{Name: "messages", Value: history}, protocol.JSONField{Name: "sessionId", Value: quote("11111111-1111-4111-8111-111111111111")}))
}
