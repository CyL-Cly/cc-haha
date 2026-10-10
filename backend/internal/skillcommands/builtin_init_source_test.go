package skillcommands

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
)

func TestInitOriginalSourceTemplatesFeatureGateAndIgnoredArguments(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	for _, feature := range []bool{false, true} {
		home := t.TempDir()
		type row struct{ Args, UserType, Flag string }
		rows := []row{{"", "external", ""}, {"ignored extra arguments", "external", "false"}, {"", "external", " true "}, {"", "external", "1"}, {"", "external", "on"}, {"", "external", "yes"}, {"", "external", "no"}, {"", "ant", "false"}, {"", "Ant", "false"}}
		inputs := []map[string]string{}
		for _, row := range rows {
			inputs = append(inputs, map[string]string{"args": row.Args, "userType": row.UserType, "flag": row.Flag})
		}
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		fixture, _ := filepath.Abs("testdata/builtin_init.fixture.ts")
		args := []string{"--no-env-file"}
		if feature {
			args = append(args, "--feature=NEW_INIT")
		}
		args = append(args, fixture)
		cmd := exec.CommandContext(ctx, bun, args...)
		cmd.Dir = home
		cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_BUILTIN_INIT_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
		input, _ := json.Marshal(map[string]any{"home": home, "rows": inputs})
		cmd.Stdin = bytes.NewReader(input)
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("Source init feature%v %v %.4500s", feature, err, out)
		}
		var source struct {
			Rows []struct {
				Metadata map[string]any
				Prompt   []struct{ Type, Text string }
			}
		}
		if err = json.Unmarshal(out, &source); err != nil || len(source.Rows) != len(rows) {
			t.Fatal("invalid original init", err, string(out))
		}
		marks := 0
		for index, row := range rows {
			definition, err := NativeInitCommand(InitCommandOptions{NewInit: feature, Environment: func(_ Invocation, key string) (string, error) {
				if key == "USER_TYPE" {
					return row.UserType, nil
				}
				return row.Flag, nil
			}, MaybeMarkProjectOnboardingComplete: func(context.Context, Invocation) error { marks++; return nil }})
			if err != nil {
				t.Fatal(err)
			}
			metadata, _ := json.Marshal(definition.Metadata())
			var actual map[string]any
			_ = json.Unmarshal(metadata, &actual)
			prompt, err := definition.Prompt(context.Background(), &row.Args, Invocation{})
			want := source.Rows[index]
			if err != nil || !reflect.DeepEqual(actual, want.Metadata) || len(want.Prompt) != 1 || want.Prompt[0].Type != "text" || prompt != want.Prompt[0].Text {
				t.Errorf("init feature%v row%d err%v metadata%v/%v prompt lengths%d/%d", feature, index, err, actual, want.Metadata, len(prompt), len(want.Prompt[0].Text))
			}
		}
		if marks != len(rows) {
			t.Fatal("original onboarding sideeffect skipped", marks)
		}
	}
}
