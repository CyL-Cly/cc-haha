package toolschema_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

type originalHost struct {
	MainLoopModel           string          `json:"mainLoopModel"`
	CompactLinePrefixKilled bool            `json:"compactLinePrefixKilled"`
	ReadLimits              json.RawMessage `json:"readLimits"`
	StrictEnabled           bool            `json:"strictEnabled"`
	FGTSEnabled             bool            `json:"fgtsEnabled"`
	FirstParty              bool            `json:"firstParty"`
	BaseURL                 *string         `json:"baseURL"`
	KillSwitch              bool            `json:"killSwitch"`
}
type originalStep struct {
	Tools        []protocol.JSONObject `json:"tools"`
	Host         originalHost          `json:"host"`
	Model        string                `json:"model"`
	DeferLoading bool                  `json:"deferLoading"`
	CacheControl json.RawMessage       `json:"cacheControl"`
	ClearCache   bool                  `json:"clearCache"`
	ParseInputs  []struct {
		Name  string              `json:"name"`
		Input protocol.JSONObject `json:"input"`
	} `json:"parseInputs"`
}
type fixtureInput struct {
	Name             string                 `json:"name"`
	HomeDir          string                 `json:"homeDir"`
	Mode             string                 `json:"mode"`
	Steps            []originalStep         `json:"steps"`
	HistorySources   []string               `json:"historySources"`
	ExtraToolSchemas []json.RawMessage      `json:"extraToolSchemas"`
	Proxy            bool                   `json:"proxy"`
	Runtime          provider.SourceRuntime `json:"runtime"`
}
type parseObservation struct {
	Success      bool   `json:"success"`
	DataSource   string `json:"dataSource,omitempty"`
	IssuesSource string `json:"issuesSource,omitempty"`
}
type schemaObservation struct {
	Schemas     []string           `json:"schemas"`
	PromptCalls map[string]int     `json:"promptCalls"`
	CacheSize   int                `json:"cacheSize"`
	Parsed      []parseObservation `json:"parsed"`
}
type fetchObservation struct {
	Path    string      `json:"path"`
	Headers [][2]string `json:"headers"`
	Body    string      `json:"body"`
}
type fixtureObservation struct {
	Records []schemaObservation `json:"records"`
	Fetches []fetchObservation  `json:"fetches"`
}

const responseWire = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"response\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"fixture-model\",\"content\":[],\"usage\":{\"input_tokens\":0,\"output_tokens\":0}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":0}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

func parseObject(source string) (protocol.JSONObject, error) {
	var object protocol.JSONObject
	err := json.Unmarshal([]byte(source), &object)
	return object, err
}
func jsStringify(value []byte) string {
	output, err := protocol.ParseAndStringifyJavaScriptJSON(value)
	if err != nil {
		panic(err)
	}
	return string(output)
}
func headerPairs(headers http.Header) [][2]string {
	result := [][2]string{}
	for name, values := range headers {
		result = append(result, [2]string{strings.ToLower(name), strings.Join(values, ", ")})
	}
	sort.Slice(result, func(i, j int) bool { return result[i][0] < result[j][0] })
	return result
}
func observeFixture(input fixtureInput) (fixtureObservation, error) {
	observation := fixtureObservation{Records: []schemaObservation{}, Fetches: []fetchObservation{}}
	requests := make(chan fetchObservation, len(input.Steps)+1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		requests <- fetchObservation{request.URL.RequestURI(), headerPairs(request.Header), string(body)}
		writer.Header().Set("content-type", "text/event-stream")
		writer.Header().Set("request-id", "schema-request")
		writer.Header().Set("connection", "keep-alive")
		_, _ = io.WriteString(writer, responseWire)
	}))
	defer server.Close()
	session := toolschema.NewSession()
	for _, step := range input.Steps {
		host := toolschema.Host{BaseURL: server.URL, MainLoopModel: step.Host.MainLoopModel, CompactLinePrefixKilled: step.Host.CompactLinePrefixKilled, ReadLimits: step.Host.ReadLimits, StrictEnabled: step.Host.StrictEnabled, FGTSEnabled: step.Host.FGTSEnabled, DisableExperimentalBetas: step.Host.KillSwitch}
		if host.MainLoopModel == "" {
			host.MainLoopModel = "deepseek-flash"
		}
		if step.Host.FirstParty {
			host.BaseURL = "https://api.anthropic.com"
		}
		if step.Host.BaseURL != nil {
			host.BaseURL = *step.Host.BaseURL
		}
		if input.Proxy {
			host.BaseURL += "/proxy/providers/fixture"
		}
		if step.ClearCache {
			session.Clear()
		}
		definitions := []toolschema.Definition{}
		for _, original := range step.Tools {
			definition, err := toolschema.ParseDefinition(original)
			if err != nil {
				return observation, err
			}
			definitions = append(definitions, definition)
		}
		if input.Mode != "request" {
			schemas, err := session.RenderBatch(definitions, toolschema.Options{Host: host, Model: step.Model, DeferLoading: step.DeferLoading, CacheControl: step.CacheControl})
			if err != nil {
				return observation, err
			}
			record := schemaObservation{Schemas: []string{}, PromptCalls: session.PromptCalls(), CacheSize: session.CacheSize(), Parsed: []parseObservation{}}
			for _, schema := range schemas {
				value, _ := schema.MarshalJSON()
				record.Schemas = append(record.Schemas, jsStringify(value))
			}
			for _, value := range step.ParseInputs {
				var selected toolschema.Definition
				for _, definition := range definitions {
					if definition.Name == value.Name {
						selected = definition
						break
					}
				}
				parsed, err := toolschema.ParseInput(selected, value.Input)
				if err != nil {
					var invalid *toolschema.ValidationError
					if !errors.As(err, &invalid) {
						return observation, err
					}
					record.Parsed = append(record.Parsed, parseObservation{IssuesSource: jsStringify(invalid.Issues)})
				} else {
					data, _ := parsed.MarshalJSON()
					record.Parsed = append(record.Parsed, parseObservation{Success: true, DataSource: jsStringify(data)})
				}
			}
			observation.Records = append(observation.Records, record)
			continue
		}
		if step.Host.FirstParty || step.Host.BaseURL != nil {
			return observation, fmt.Errorf("first-party schema profile cannot send requests")
		}
		history := []protocol.JSONObject{}
		sources := input.HistorySources
		if sources == nil {
			sources = []string{`{"type":"user","uuid":"original","message":{"role":"user","content":"question"}}`}
		}
		for _, source := range sources {
			envelope, err := parseObject(source)
			if err != nil {
				return observation, err
			}
			history = append(history, envelope)
		}
		env := map[string]string{"ANTHROPIC_BASE_URL": host.BaseURL, "ANTHROPIC_API_KEY": "offline-fixture-key"}
		if step.Host.KillSwitch {
			env["CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS"] = "true"
		}
		model := step.Model
		if model == "" {
			model = "deepseek-flash"
		}
		inputContext := requestcontext.Context{History: history, SystemPrompt: []json.RawMessage{}, Tools: step.Tools, Thinking: requestcontext.Thinking{Type: "disabled"}, Options: requestcontext.Options{Model: model, QuerySource: "sdk", IsNonInteractiveSession: true, ExtraToolSchemas: input.ExtraToolSchemas}, Host: requestcontext.Host{Profile: requestcontext.DirectRelayProfile, Env: env, DeviceID: "fixture-device", SessionID: "00000000-0000-4000-8000-000000000000", ToolSchemas: session, ToolSchemaHost: &host}}
		result, err := requestcontext.Build(inputContext, requestcontext.Dependencies{UUID: func() string { return "schema-id" }, Now: func() time.Time { return time.UnixMilli(1700000000000) }})
		if err != nil {
			return observation, err
		}
		key := "offline-fixture-key"
		options := provider.ResolveDirectClientOptions(provider.DirectClientInput{Env: env, FallbackAPIKey: &key, SessionID: inputContext.Host.SessionID, Runtime: input.Runtime})
		config := options.StreamConfig()
		finalize := config.FinalizeHeaders
		config.FinalizeHeaders = func(requestURL string, headers http.Header) (http.Header, error) {
			resolved, err := finalize(requestURL, headers)
			if err != nil {
				return nil, err
			}
			parsed, err := url.Parse(requestURL)
			if err != nil {
				return nil, err
			}
			observation.Fetches = append(observation.Fetches, fetchObservation{parsed.RequestURI(), headerPairs(resolved), string(result.PreparedRequest.Body)})
			return resolved, nil
		}
		stream, err := provider.NewAnthropic(config).OpenMessagesStream(context.Background(), result.PreparedRequest)
		if err != nil {
			return observation, err
		}
		for {
			_, err = stream.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				stream.Close()
				return observation, err
			}
		}
		stream.Close()
		wire := <-requests
		resolved := observation.Fetches[len(observation.Fetches)-1]
		if wire.Path != resolved.Path || wire.Body != resolved.Body {
			return observation, fmt.Errorf("actual request differs from final-fetch capture")
		}
		wireHeaders := map[string]string{}
		for _, pair := range wire.Headers {
			wireHeaders[pair[0]] = pair[1]
		}
		for _, pair := range resolved.Headers {
			if wireHeaders[pair[0]] != pair[1] {
				return observation, fmt.Errorf("actual header differs: %s", pair[0])
			}
		}
	}
	return observation, nil
}
func TestToolSchemaFixture(t *testing.T) {
	if os.Getenv("CC_HAHA_TOOL_SCHEMA_FIXTURE") != "1" {
		t.Skip("private parity entry only")
	}
	source, err := io.ReadAll(os.Stdin)
	if err != nil {
		t.Fatal(err)
	}
	var input fixtureInput
	if err = json.Unmarshal(source, &input); err != nil {
		t.Fatal(err)
	}
	home, err := filepath.EvalSymlinks(input.HomeDir)
	if err != nil {
		t.Fatal(err)
	}
	allocation, err := filepath.EvalSymlinks(os.Getenv("CC_HAHA_FIXTURE_TEMP_ROOT"))
	if err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	cwd, _ = filepath.EvalSymlinks(cwd)
	if input.HomeDir != os.Getenv("HOME") || input.HomeDir != os.Getenv("USERPROFILE") || home != cwd || !strings.HasPrefix(home, allocation+string(filepath.Separator)) || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(input.HomeDir, ".claude") || os.Getenv("BUN_OPTIONS") != "--no-env-file" || os.Getenv("NODE_ENV") != "production" {
		t.Fatal("tool schema requires disposable HOME/config")
	}
	observation, err := observeFixture(input)
	if err != nil {
		t.Fatal(err)
	}
	output, err := json.Marshal(map[string]any{"observation": observation})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("TOOL_SCHEMA_RESULT:%s\n", output)
}
