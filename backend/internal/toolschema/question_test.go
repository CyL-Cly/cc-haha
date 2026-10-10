package toolschema

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
)

func TestQuestionSchemaPromptAndRefinementsMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun runner not configured")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	_ = os.Mkdir(config, 0700)
	_ = os.Mkdir(filepath.Join(home, "tmp"), 0700)
	question := `{"question":"Where?","header":"Scope","options":[{"label":"A","description":"First"},{"label":"B","description":"Second"}]}`
	valid := `{"questions":[` + question + `]}`
	inputs := []string{
		valid, `{"questions":[` + question + `],"metadata":{"source":"remember","autoAnswered":true,"unknown":true}}`,
		`{"questions":[` + question + `],"answers":{"Where?":"A"},"annotations":{"Where?":{"preview":"<div>A</div>","notes":"first","unknown":true}}}`,
		`{"questions":[` + question + `],"answers":{"2":"A","1":"B","__proto__":"C","constructor":"D","\ud800":"E"}}`,
		`{"questions":[` + question + `],"answers":{"__proto__":3},"annotations":{"__proto__":false}}`,
		`{"questions":[{"question":"","header":"much longer than 12","options":[{"label":"","description":"","preview":""},{"label":"B","description":""}],"unknown":true}],"metadata":{}}`,
		`{"questions":[` + question + `,` + question + `]}`,
		`{"questions":[{"question":"Q","header":"H","options":[{"label":"A","description":"one"},{"label":"A","description":"two"}]}]}`,
		`{"questions":[]}`, `{"questions":[` + question + `,` + question + `,` + question + `,` + question + `,` + question + `]}`,
		`{"questions":[{"question":"Q","header":"H","options":[]}]}`, `{"questions":[{"question":"Q","header":"H","options":[{"label":"A","description":"D"}]}]}`,
		`{"questions":[{"question":"Q","header":"H","options":[{"label":"A","description":"D"},{"label":"B","description":"D"},{"label":"C","description":"D"},{"label":"D","description":"D"},{"label":"E","description":"D"}]}]}`,
		`null`, `{}`, `{"questions":null}`, `{"questions":[null,{}]}`, `{"questions":[{"question":3,"header":true,"options":[null,{}],"multiSelect":"true"}],"answers":{"x":3},"annotations":{"y":false},"metadata":null,"unknown":true}`,
		`{"questions":[` + question + `],"answers":[]}`, `{"questions":[` + question + `],"annotations":{"x":{"notes":3,"preview":null}}}`, `{"questions":[` + question + `],"extra":true}`,
		`{"questions":[` + question + `,` + question + `],"extra":true}`, `{"questions":[` + question + `],"metadata":{"source":3}}`,
		`{"questions":[{"question":"Q","header":"H","options":[{"label":"A","description":"D"},{"label":"B","description":"D"}],"multiSelect":null}]}`,
	}
	steps := []map[string]any{}
	for i, value := range inputs {
		steps = append(steps, map[string]any{"name": i, "inputSource": value})
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-question.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_QUESTION_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"homeDir": home, "steps": steps})
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual Source %v %.2000s", err, stderr.String())
	}
	var source struct {
		Schemas      []string
		Observations []struct {
			Success                  bool
			DataSource, IssuesSource string
		}
		Capabilities struct{ Blocked []string }
	}
	if json.Unmarshal(output, &source) != nil {
		t.Fatalf("Source JSON %.2000s", output)
	}
	if len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("fixture escaped %v", source.Capabilities.Blocked)
	}
	definition, err := NativeBuiltinDefinition("AskUserQuestion")
	if err != nil {
		t.Fatal(err)
	}
	for i, format := range []string{"", "markdown", "html"} {
		schemas, err := NewSession().RenderBatch([]Definition{definition}, Options{Host: Host{BaseURL: "http://127.0.0.1:1", QuestionPreviewFormat: format}, Model: "deepseek-flash"})
		if err != nil {
			t.Fatal(err)
		}
		if !jsonSourcesEqual(raw(schemas[0]), []byte(source.Schemas[i])) {
			t.Errorf("format%q schema Go=%s Source=%s", format, raw(schemas[0]), source.Schemas[i])
		}
	}
	if len(source.Observations) != len(inputs) {
		t.Fatal("Source observations")
	}
	for i, input := range inputs {
		parsed, err := ParseInputJSON(definition, []byte(input))
		observation := source.Observations[i]
		if observation.Success {
			if err != nil || !jsonSourcesEqual(raw(parsed), []byte(observation.DataSource)) {
				t.Errorf("case%d parsed Go=%s err=%v Source=%s", i, raw(parsed), err, observation.DataSource)
			}
		} else {
			var failure *ValidationError
			if !errors.As(err, &failure) {
				t.Errorf("case%d accepted Source rejection %v", i, err)
			} else if !jsonSourcesEqual(failure.Issues, []byte(observation.IssuesSource)) {
				t.Errorf("case%d issues Go=%s Source=%s", i, failure.Issues, observation.IssuesSource)
			}
		}
	}
}
