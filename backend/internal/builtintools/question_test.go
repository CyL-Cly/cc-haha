package builtintools

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
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

func TestQuestionPermissionCallResultsAndHTMLMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun runner not configured")
	}
	s, owner, home, permissions := fixture(t)
	permissions.Set("alwaysAllowRules", raw(map[string][]string{"cliArg": {"AskUserQuestion"}}))
	question := `{"question":"Where?","header":"Scope","options":[{"label":"A","description":"First"},{"label":"B","description":"Second"}]}`
	type step struct {
		Name             string              `json:"name"`
		InputSource      string              `json:"inputSource"`
		PreviewFormat    string              `json:"previewFormat,omitempty"`
		PermissionUpdate protocol.JSONObject `json:"permissionUpdate,omitzero"`
	}
	steps := []step{
		{Name: "empty", InputSource: `{"questions":[` + question + `]}`},
		{Name: "predefined", InputSource: `{"questions":[` + question + `],"answers":{"Where?":"A"}}`},
		{Name: "free text", InputSource: `{"questions":[` + question + `],"answers":{"Where?":"Wait. Explain first."}}`},
		{Name: "notes", InputSource: `{"questions":[` + question + `],"answers":{"Where?":"A"},"annotations":{"Where?":{"notes":"Need more details","preview":"<div>A</div>"}}}`},
		{Name: "blank notes", InputSource: `{"questions":[` + question + `],"answers":{"Where?":"A"},"annotations":{"Where?":{"notes":" \ufeff ","preview":"<div>A</div>"}}}`},
		{Name: "unknown answer", InputSource: `{"questions":[` + question + `],"answers":{"Where?":"A","Unknown?":"B"}}`},
		{Name: "unanswered", InputSource: `{"questions":[` + question + `,{"question":"Other?","header":"Other","options":[{"label":"A","description":"A"},{"label":"B","description":"B"}]}],"answers":{"Where?":"A"}}`},
		{Name: "model auto stripped", InputSource: `{"questions":[` + question + `],"answers":{"Where?":"A"},"metadata":{"source":"remember","autoAnswered":true}}`},
		{Name: "trusted timeout", InputSource: `{"questions":[` + question + `]}`, PermissionUpdate: input(`{"answers":{"Where?":"A"},"metadata":{"source":"remember","autoAnswered":true}}`)},
		{Name: "multi", InputSource: `{"questions":[{"question":"What?","header":"Multi","options":[{"label":"A","description":"A"},{"label":"B","description":"B"}],"multiSelect":true}],"answers":{"What?":" A , B "}}`},
		{Name: "empty multi", InputSource: `{"questions":[{"question":"What?","header":"Multi","options":[{"label":"A","description":"A"},{"label":"B","description":"B"}],"multiSelect":true}],"answers":{"What?":", \ufeff ,"}}`},
		{Name: "UTF16", InputSource: `{"questions":[{"question":"\ud800?","header":"Surrogate","options":[{"label":"\ud801","description":"A"},{"label":"B","description":"B"}]}],"answers":{"\ud800?":"\ud801"},"annotations":{"\ud800?":{"notes":"\ud802"}}}`},
	}
	for _, preview := range []string{"<div style=\"color:red\">safe</div>", "<html>document</html>", "<!DOCTYPE html><div>doc</div>", "<script>x</script>", "<\u00a0STYLE>x</STYLE>", "plain text", "", "<div onclick=\"x\">inline</div>"} {
		value := input(`{"questions":[` + question + `]}`)
		var questions []protocol.JSONObject
		_ = json.Unmarshal(value.Get("questions"), &questions)
		var options []protocol.JSONObject
		_ = json.Unmarshal(questions[0].Get("options"), &options)
		options[0].Set("preview", quote(preview))
		questions[0].Set("options", raw(options))
		value.Set("questions", raw(questions))
		steps = append(steps, step{Name: "html " + preview, InputSource: string(raw(value)), PreviewFormat: "html"})
	}
	fixturePath, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-question.fixture.ts")
	config := filepath.Join(home, ".claude")
	_ = os.Mkdir(config, 0700)
	_ = os.Mkdir(filepath.Join(home, "tmp"), 0700)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixturePath)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_QUESTION_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"homeDir": home, "steps": steps}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual Source %v %.2000s", err, stderr.String())
	}
	var source struct {
		Observations []struct {
			Success                                                                    bool
			DataSource, PermissionSource, ValidationSource, ResultSource, MappedSource string
		}
		Capabilities struct{ Blocked []string }
	}
	if json.Unmarshal(output, &source) != nil || len(source.Observations) != len(steps) {
		t.Fatalf("Source JSON %.1000s", output)
	}
	if len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("fixture escaped %v", source.Capabilities.Blocked)
	}
	definition, _ := toolschema.NativeBuiltinDefinition("AskUserQuestion")
	for i, item := range steps {
		t.Run(item.Name, func(t *testing.T) {
			in, err := toolschema.ParseInputJSON(definition, []byte(item.InputSource))
			if err != nil {
				t.Fatal(err)
			}
			observation := source.Observations[i]
			err = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				s.options.QuestionPreviewFormat = item.PreviewFormat
				decision, err := s.PermissionDecision(scope, "AskUserQuestion", &in)
				if err != nil {
					return err
				}
				if decision.Behavior != "ask" || string(decision.Message) != `"Answer questions?"` {
					t.Fatal("question skipped required user interaction")
				}
				validation := protocol.NewJSONObject(field("result", true))
				if err = s.Validate(context.Background(), "AskUserQuestion", in, scope); err != nil {
					validation = protocol.NewJSONObject(field("result", false), protocol.JSONField{Name: "message", Value: quote(err.Error())}, field("errorCode", 1))
				}
				if !questionJSONEqual(raw(validation), []byte(observation.ValidationSource)) {
					t.Errorf("validation Go=%s Source=%s", raw(validation), observation.ValidationSource)
				}
				if item.PermissionUpdate.IsObject() {
					in.Assign(item.PermissionUpdate)
				}
				if !questionJSONEqual(raw(in), []byte(observation.DataSource)) {
					t.Errorf("input Go=%s Source=%s", raw(in), observation.DataSource)
				}
				data, err := s.Call(context.Background(), "AskUserQuestion", in, scope, item.PermissionUpdate.IsObject())
				if err != nil {
					return err
				}
				if !questionJSONEqual(data, []byte(observation.ResultSource)) {
					t.Errorf("call Go=%s Source=%s", data, observation.ResultSource)
				}
				mapped, err := MapResult("AskUserQuestion", data, quote("actual-question-id"))
				if err != nil {
					return err
				}
				if !questionJSONEqual(raw(mapped), []byte(observation.MappedSource)) {
					t.Errorf("map Go=%s Source=%s", raw(mapped), observation.MappedSource)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func questionJSONEqual(a, b json.RawMessage) bool {
	return reflect.DeepEqual(protocol.StringifyValidatedJavaScriptJSON(a), protocol.StringifyValidatedJavaScriptJSON(b))
}

func TestQuestionCannotBypassAndExplicitDenyOmitsCatalog(t *testing.T) {
	s, owner, _, permissions := fixture(t)
	in := input(`{"questions":[]}`)
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		for _, mode := range []string{"default", "acceptEdits", "plan", "bypassPermissions", "dontAsk", "auto"} {
			permissions.Set("mode", quote(mode))
			permissions.Set("alwaysAllowRules", raw(map[string][]string{"userSettings": {"AskUserQuestion"}}))
			decision, err := s.PermissionDecision(scope, "AskUserQuestion", &in)
			if err != nil {
				return err
			}
			if decision.Behavior != "ask" {
				t.Errorf("mode %s bypassed questions", mode)
			}
		}
		permissions.Set("alwaysDenyRules", raw(map[string][]string{"userSettings": {"AskUserQuestion"}}))
		decision, err := s.PermissionDecision(scope, "AskUserQuestion", &in)
		if err != nil {
			return err
		}
		if decision.Behavior != "deny" {
			t.Fatal("deny ignored")
		}
		defs, tools, err := s.Catalog(scope)
		if err != nil {
			return err
		}
		for _, def := range defs {
			if def.Name == "AskUserQuestion" {
				t.Fatal("denied question catalog")
			}
		}
		for _, tool := range tools {
			if tool.Name == "AskUserQuestion" {
				t.Fatal("denied question handler")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
