package attachments_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/attachments"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

type originalCase struct{ Name, Kind, Source, Tools string }
type errorObservation struct {
	Class       string `json:"class"`
	Name        string `json:"name"`
	Message     string `json:"message"`
	MessageJSON string `json:"messageJSON"`
}
type observation struct {
	Name      string              `json:"name"`
	Before    string              `json:"before"`
	After     string              `json:"after"`
	Reordered []string            `json:"reordered"`
	Output    []string            `json:"output"`
	Error     *errorObservation   `json:"error"`
	Trace     []map[string]string `json:"trace"`
}

func js(raw json.RawMessage) string {
	result, err := protocol.ParseAndStringifyJavaScriptJSON(raw)
	if err != nil {
		panic(err)
	}
	return string(result)
}
func objects(raw json.RawMessage) []protocol.JSONObject {
	var value []protocol.JSONObject
	if err := json.Unmarshal(raw, &value); err != nil {
		panic(err)
	}
	return value
}
func sources(input []protocol.JSONObject) []string {
	result := []string{}
	for _, value := range input {
		raw, err := value.MarshalJSON()
		if err != nil {
			panic(err)
		}
		result = append(result, js(raw))
	}
	return result
}
func runCase(row originalCase) observation {
	result := observation{Name: row.Name, Before: js(json.RawMessage(row.Source)), After: js(json.RawMessage(row.Source)), Trace: []map[string]string{}}
	count := 0
	uuid := func() string {
		count++
		value := fmt.Sprintf("skills-id-%d", count)
		result.Trace = append(result.Trace, map[string]string{"kind": "uuid", "value": value})
		return value
	}
	now := func() time.Time {
		result.Trace = append(result.Trace, map[string]string{"kind": "date", "value": "2023-11-14T22:13:20.000Z"})
		return time.UnixMilli(1700000000000)
	}
	factory, _ := messages.NewFactory(uuid, now)
	var output []protocol.JSONObject
	var err error
	if row.Kind == "projection" {
		output, err = attachments.Project(json.RawMessage(row.Source), factory)
	} else {
		original := objects(json.RawMessage(row.Source))
		result.Reordered = sources(attachments.ReorderForAPI(original))
		input := requestcontext.Context{History: original, Thinking: requestcontext.Thinking{Type: "disabled"}, Options: requestcontext.Options{Model: "fixture-model", QuerySource: "sdk", IsNonInteractiveSession: true}, Host: requestcontext.Host{Profile: requestcontext.DirectRelayProfile, Env: map[string]string{"ANTHROPIC_BASE_URL": "http://127.0.0.1:1"}, DeviceID: "skills-device", SessionID: "skills-session"}}
		if row.Tools == "write" {
			var definition protocol.JSONObject
			_ = json.Unmarshal([]byte(`{"kind":"builtin","name":"Write"}`), &definition)
			input.Tools = []protocol.JSONObject{definition}
			input.Host.ToolSchemas = toolschema.NewSession()
			input.Host.ToolSchemaHost = &toolschema.Host{MainLoopModel: "fixture-model"}
		}
		prepared, failure := requestcontext.Prepare(input, requestcontext.Dependencies{UUID: uuid, Now: now})
		err = failure
		if failure == nil {
			output = prepared.NormalizedHistory()
		}
		raw, _ := json.Marshal(original)
		result.After = js(raw)
	}
	if err != nil {
		var malformed *attachments.MalformedAttachmentError
		class, name := "Error", "Error"
		messageJSON, _ := json.Marshal(err.Error())
		if errors.As(err, &malformed) {
			class = malformed.Class
			name = malformed.Class
			messageJSON = malformed.MessageJSON
		}
		result.Error = &errorObservation{Class: class, Name: name, Message: err.Error(), MessageJSON: string(messageJSON)}
	} else {
		result.Output = sources(output)
	}
	return result
}
func TestRetainedSkillsFixture(t *testing.T) {
	if os.Getenv("CC_HAHA_RETAINED_SKILLS_FIXTURE") != "1" {
		t.Skip("Source parity child only")
	}
	var input struct {
		Home string
		Rows []originalCase
	}
	decoder := json.NewDecoder(os.Stdin)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		t.Fatal(err)
	}
	home := input.Home
	cwd, _ := os.Getwd()
	canonical, _ := filepath.EvalSymlinks(home)
	canonicalCwd, _ := filepath.EvalSymlinks(cwd)
	info, err := os.Lstat(home)
	allocation, rootErr := filepath.EvalSymlinks(os.Getenv("CC_HAHA_FIXTURE_TEMP_ROOT"))
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || home != os.Getenv("HOME") || canonical != canonicalCwd || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(home, ".claude") || rootErr != nil || !strings.HasPrefix(canonical, allocation+string(os.PathSeparator)) {
		t.Fatal("Retained Skills requires disposable HOME/config")
	}
	output := []observation{}
	for _, row := range input.Rows {
		output = append(output, runCase(row))
	}
	raw, err := json.Marshal(map[string]any{"observations": output})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("RETAINED_SKILLS_RESULT:%s\n", raw)
}
