package requestcontext

import (
	"context"
	"encoding/json"
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
)

type fixtureInput struct {
	Name           string                 `json:"name"`
	HomeDir        string                 `json:"homeDir"`
	HistorySources []string               `json:"historySources"`
	SystemSources  []string               `json:"systemSources"`
	Env            map[string]string      `json:"env"`
	Options        json.RawMessage        `json:"options"`
	Thinking       *Thinking              `json:"thinking"`
	SDKBetas       []string               `json:"sdkBetas"`
	TTLEligible    bool                   `json:"ttlEligible"`
	TTLAllowlist   []string               `json:"ttlAllowlist"`
	Workload       string                 `json:"workload"`
	Proxy          bool                   `json:"proxy"`
	Runtime        provider.SourceRuntime `json:"runtime"`
}
type fetchObservation struct {
	Path, Method string
	Headers      [][2]string
	Body         string
}

func (value fetchObservation) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Path    string      `json:"path"`
		Method  string      `json:"method"`
		Headers [][2]string `json:"headers"`
		Body    string      `json:"body"`
	}{value.Path, value.Method, value.Headers, value.Body})
}

type historyObservation struct {
	Type          string          `json:"type"`
	UUID          json.RawMessage `json:"uuid"`
	MessageSource string          `json:"messageSource"`
}
type fixtureObservation struct {
	Fetches           []fetchObservation   `json:"fetches"`
	NormalizedHistory []historyObservation `json:"normalizedHistory"`
	HistoryBefore     []string             `json:"historyBefore"`
	HistoryAfter      []string             `json:"historyAfter"`
	UUIDCalls         int                  `json:"uuidCalls"`
	PreviousRequestID json.RawMessage      `json:"previousRequestID"`
	wireRequests      []fetchObservation
}

const responseWire = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"response\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"fixture-model\",\"content\":[],\"usage\":{\"input_tokens\":0,\"output_tokens\":0}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":0}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

func fixtureContext(input fixtureInput, origin string) (Context, error) {
	options := Options{Model: "fixture-model", QuerySource: "sdk", IsNonInteractiveSession: true}
	if len(input.Options) > 0 {
		if err := json.Unmarshal(input.Options, &options); err != nil {
			return Context{}, err
		}
	}
	thinking := Thinking{Type: "disabled"}
	if input.Thinking != nil {
		thinking = *input.Thinking
	}
	history := make([]protocol.JSONObject, 0, len(input.HistorySources))
	for _, source := range input.HistorySources {
		envelope, err := parseObject(json.RawMessage(source))
		if err != nil {
			return Context{}, err
		}
		history = append(history, envelope)
	}
	env := map[string]string{"ANTHROPIC_BASE_URL": origin, "ANTHROPIC_API_KEY": "offline-fixture-key"}
	for name, value := range input.Env {
		env[name] = value
	}
	if input.Proxy {
		env["ANTHROPIC_BASE_URL"] += "/proxy/providers/fixture"
	}
	system := make([]json.RawMessage, 0, len(input.SystemSources))
	for _, source := range input.SystemSources {
		system = append(system, json.RawMessage(source))
	}
	return Context{History: history, SystemPrompt: system, Thinking: thinking, Options: options, Host: Host{Profile: DirectRelayProfile, Env: env, DeviceID: "fixture-device", SessionID: "00000000-0000-4000-8000-000000000000", Workload: input.Workload, SDKBetas: input.SDKBetas, TTLEligible: input.TTLEligible, TTLAllowlist: input.TTLAllowlist}}, nil
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
	observation := fixtureObservation{Fetches: []fetchObservation{}, NormalizedHistory: []historyObservation{}, HistoryBefore: []string{}, HistoryAfter: []string{}}
	requests := make(chan fetchObservation, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		headers := request.Header.Clone()
		headers.Set("host", request.Host)
		requests <- fetchObservation{request.URL.RequestURI(), request.Method, headerPairs(headers), string(body)}
		writer.Header().Set("content-type", "text/event-stream")
		writer.Header().Set("request-id", "response-request")
		writer.Header().Set("connection", "keep-alive")
		_, _ = io.WriteString(writer, responseWire)
	}))
	defer server.Close()
	inputContext, err := fixtureContext(input, server.URL)
	if err != nil {
		return observation, err
	}
	for _, envelope := range inputContext.History {
		raw, err := stringify(rawObject(envelope))
		if err != nil {
			return observation, err
		}
		observation.HistoryBefore = append(observation.HistoryBefore, string(raw))
	}
	uuid := func() string { observation.UUIDCalls++; return fmt.Sprintf("context-id-%d", observation.UUIDCalls) }
	result, err := Build(inputContext, Dependencies{UUID: uuid, Now: func() time.Time { return time.UnixMilli(1700000000000) }})
	if err != nil {
		return observation, err
	}
	observation.PreviousRequestID = result.PreviousRequestID
	if observation.PreviousRequestID == nil {
		// Source's logAPIQuery observer explicitly records absent IDs as null.
		// Preserve that observation shape without changing the Build result.
		observation.PreviousRequestID = json.RawMessage("null")
	}
	for _, envelope := range result.NormalizedHistory {
		raw, err := stringify(envelope.Get("message"))
		if err != nil {
			return observation, err
		}
		identity := envelope.Get("uuid")
		if identity == nil {
			identity = json.RawMessage("null")
		}
		observation.NormalizedHistory = append(observation.NormalizedHistory, historyObservation{envelope.String("type"), identity, string(raw)})
	}
	clientOptions := provider.ResolveDirectClientOptions(provider.DirectClientInput{Env: inputContext.Host.Env, FallbackAPIKey: stringPointer("offline-fixture-key"), SessionID: inputContext.Host.SessionID, Workload: inputContext.Host.Workload, Runtime: input.Runtime, RequestID: uuid})
	config := clientOptions.StreamConfig()
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
		observation.Fetches = append(observation.Fetches, fetchObservation{parsed.RequestURI(), "POST", headerPairs(resolved), string(result.PreparedRequest.Body)})
		return resolved, nil
	}
	stream, err := provider.NewAnthropic(config).OpenMessagesStream(context.Background(), result.PreparedRequest)
	if err != nil {
		return observation, err
	}
	defer stream.Close()
	for {
		_, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return observation, err
		}
	}
	wire := <-requests
	observation.wireRequests = []fetchObservation{wire}
	if len(observation.Fetches) != 1 || wire.Path != observation.Fetches[0].Path || wire.Method != "POST" || wire.Body != observation.Fetches[0].Body {
		return observation, fmt.Errorf("actual loopback request differs from final fetch observation")
	}
	wireHeaders := map[string]string{}
	for _, pair := range wire.Headers {
		wireHeaders[pair[0]] = pair[1]
	}
	for _, pair := range observation.Fetches[0].Headers {
		if wireHeaders[pair[0]] != pair[1] {
			return observation, fmt.Errorf("actual loopback header differs: %s", pair[0])
		}
	}
	for _, envelope := range inputContext.History {
		raw, _ := stringify(rawObject(envelope))
		observation.HistoryAfter = append(observation.HistoryAfter, string(raw))
	}
	return observation, nil
}
func stringPointer(value string) *string { return &value }
func TestRequestContextFixture(t *testing.T) {
	if os.Getenv("CC_HAHA_REQUEST_CONTEXT_FIXTURE") != "1" {
		t.Skip("private parity entry only")
	}
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		t.Fatal(err)
	}
	var input fixtureInput
	if err = json.Unmarshal(raw, &input); err != nil {
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
		t.Fatal("request context requires disposable HOME/config")
	}
	observation, err := observeFixture(input)
	if err != nil {
		t.Fatal(err)
	}
	output, err := json.Marshal(map[string]any{"observation": observation, "goOnly": map[string]any{"wireRequests": observation.wireRequests}})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("REQUEST_CONTEXT_RESULT:%s\n", output)
}
