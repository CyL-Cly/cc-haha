package sdkhost

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/goalstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeGoalJSXMessagesMatchActualSourceAndSkipInputAttachments(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	args := []string{"", "ship task", "replace target", "status", "clear", "clear", "中文 😀"}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../goalstate/testdata/goal-command.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_GOAL_COMMAND_FIXTURE=1", "PATH=/usr/bin:/bin", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "arguments": args, "interactive": false}))
	data, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(data))
	}
	var source struct {
		Rows []struct {
			Messages    []protocol.JSONObject
			ShouldQuery bool
		}
	}
	if err = json.Unmarshal(data, &source); err != nil {
		t.Fatal(err, string(data))
	}
	owner := cooperative.New()
	defer owner.Close()
	factory, _ := messages.NewFactory(func() string { return "fixture" }, func() time.Time { return time.Unix(0, 0) })
	state, _ := goalstate.New(goalstate.Options{Owner: owner, Add: func(*cooperative.Scope, string, *goalstate.Hook, func(*cooperative.Scope) error) error { return nil }, Remove: func(*cooperative.Scope, string, *goalstate.Hook) error { return nil }})
	host := &Host{options: Options{Factory: factory, NowMillis: func() float64 { return 0 }, Print: &PrintOptions{NativeGoalCommand: func(_ context.Context, scope *cooperative.Scope, args string) (string, bool, error) {
		parsed, err := goalstate.Parse(args)
		if err != nil {
			return err.Error(), false, nil
		}
		if parsed.Type == "clear" {
			goal, err := state.Clear(scope, "session")
			if err != nil {
				return "", false, err
			}
			if goal == nil {
				return "No active goal.", false, nil
			}
			return "Goal cleared: " + goal.Objective, false, nil
		}
		goal, err := state.Set(scope, "session", parsed.Objective, 0)
		if err != nil {
			return "", false, err
		}
		return "Goal set: " + goal.Objective, true, nil
	}}}}
	for i, arg := range args {
		err = owner.Run(ctx, func(scope *cooperative.Scope) error {
			result, err := host.processNativeGoalInput(scope, arg)
			if err != nil {
				return err
			}
			actual := []protocol.JSONObject{}
			for _, record := range result.Messages {
				copy := record.Clone()
				copy.Delete("uuid")
				copy.Delete("timestamp")
				actual = append(actual, copy)
			}
			var a, b any
			_ = json.Unmarshal(raw(actual), &a)
			_ = json.Unmarshal(raw(source.Rows[i].Messages), &b)
			if !reflect.DeepEqual(a, b) || result.NoQuery == source.Rows[i].ShouldQuery || !result.AttachmentsHandled {
				t.Errorf("case%d Go%s Source%s noQuery%v", i, raw(actual), raw(source.Rows[i]), result.NoQuery)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
