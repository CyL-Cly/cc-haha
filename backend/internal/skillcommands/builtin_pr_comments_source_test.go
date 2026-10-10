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

func TestBuiltinPRCommentsActualSourceMetadataAndBothUserProfiles(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	type row struct{ Args, UserType string }
	rows := []row{{"", ""}, {"", "external"}, {"   ", "external"}, {"Additional focus\nquoted 'text' $ARGUMENTS", "external"}, {"中文😀", "external"}, {"", "ant"}, {"any input is ignored", "ant"}, {"  visible ", "Ant"}}
	inputs := []map[string]string{}
	for _, item := range rows {
		inputs = append(inputs, map[string]string{"args": item.Args, "userType": item.UserType})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	fixture, _ := filepath.Abs("testdata/builtin_pr_comments.fixture.ts")
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_BUILTIN_PR_COMMENTS_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "NODE_ENV=production"}
	input, _ := json.Marshal(map[string]any{"home": home, "rows": inputs})
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	var source struct {
		Rows []struct {
			Metadata map[string]any
			Prompt   []struct{ Type, Text string }
		}
	}
	if err = json.Unmarshal(out, &source); err != nil || len(source.Rows) != len(rows) {
		t.Fatal("invalid original PR commands", err, string(out))
	}
	for index, row := range rows {
		definition, err := NativePRCommentsCommand(func(Invocation, string) (string, error) { return row.UserType, nil })
		if err != nil {
			t.Fatal(err)
		}
		metadata, _ := json.Marshal(definition.Metadata())
		var actual map[string]any
		_ = json.Unmarshal(metadata, &actual)
		prompt, err := definition.Prompt(ctx, &row.Args, Invocation{})
		want := source.Rows[index]
		if err != nil || !reflect.DeepEqual(actual, want.Metadata) || len(want.Prompt) != 1 || want.Prompt[0].Type != "text" || prompt != want.Prompt[0].Text {
			t.Errorf("PR%d err%v metadata%v/%v prompt Go%q Source%v", index, err, actual, want.Metadata, prompt, want.Prompt)
		}
	}
}
