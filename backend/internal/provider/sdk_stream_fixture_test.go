package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// These fixtures are source observations of SDK 0.80.0/Bun 1.3.14. The test
// adapter delegates transport and dispatch to production OpenMessagesStream;
// stringify and numeric leaf records are only an observation boundary.
type sdkStreamScenario struct {
	Name             string      `json:"name"`
	WireBytes        []int       `json:"wireBytes"`
	Chunks           []int       `json:"chunks"`
	Headers          [][2]string `json:"headers"`
	Status           int         `json:"status"`
	DeepArray        bool        `json:"deepArray"`
	DeepErrorNumbers string      `json:"deepErrorNumbers"`
	Home             string      `json:"home"`
}

type sdkStreamNumber struct {
	Value        string `json:"value"`
	NegativeZero bool   `json:"negativeZero"`
}

func sdkStreamStringify(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	if !json.Valid(raw) && len(sdkJSONSyntaxUnits(string(raw))) != 0 {
		t.Fatal("fixture stringify requires complete Source-compatible JSON validation")
	}
	return string(protocol.StringifyValidatedJavaScriptJSON(raw))
}

func sdkStreamNumberLeaves(t *testing.T, raw json.RawMessage) map[string]sdkStreamNumber {
	t.Helper()
	result := map[string]sdkStreamNumber{}
	var visit func(json.RawMessage, []json.RawMessage)
	visit = func(raw json.RawMessage, path []json.RawMessage) {
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 {
			return
		}
		switch raw[0] {
		case '{':
			var object protocol.JSONObject
			if err := object.UnmarshalJSON(raw); err != nil {
				t.Fatal(err)
			}
			// Resolve duplicate keys before walking, retaining original numbers
			// and UTF-16 property identity. JSON.parse uses the last value.
			ordered, err := object.MarshalJSON()
			if err != nil {
				t.Fatal(err)
			}
			decoder := json.NewDecoder(bytes.NewReader(ordered))
			_, _ = decoder.Token()
			for decoder.More() {
				start := decoder.InputOffset()
				_, _ = decoder.Token()
				key := ordered[start:decoder.InputOffset()]
				key = key[bytes.IndexByte(key, '"'):]
				var value json.RawMessage
				_ = decoder.Decode(&value)
				visit(value, append(append([]json.RawMessage{}, path...), key))
			}
		case '[':
			var values []json.RawMessage
			if err := json.Unmarshal(raw, &values); err != nil {
				t.Fatal(err)
			}
			for index, value := range values {
				visit(value, append(append([]json.RawMessage{}, path...), json.RawMessage(strconv.Itoa(index))))
			}
		default:
			if raw[0] != '-' && (raw[0] < '0' || raw[0] > '9') {
				return
			}
			value, err := strconv.ParseFloat(string(raw), 64)
			if err != nil && !math.IsInf(value, 0) {
				t.Fatal(err)
			}
			quoted, err := protocol.JavaScriptStringJSON(raw)
			if err != nil {
				t.Fatal(err)
			}
			var text string
			_ = json.Unmarshal(quoted, &text)
			pathJSON, _ := json.Marshal(path)
			result[sdkStreamStringify(t, pathJSON)] = sdkStreamNumber{Value: text, NegativeZero: value == 0 && math.Signbit(value)}
		}
	}
	visit(raw, []json.RawMessage{})
	return result
}

func sdkStreamHeaders(headers http.Header) [][2]string {
	combined := map[string][]string{}
	for name, values := range headers {
		key := strings.ToLower(name)
		for _, value := range values {
			combined[key] = append(combined[key], strings.Trim(value, " \t"))
		}
	}
	names := make([]string, 0, len(combined))
	for name := range combined {
		names = append(names, name)
	}
	sort.Strings(names)
	pairs := make([][2]string, 0, len(names))
	for _, name := range names {
		if name == "set-cookie" {
			for _, value := range combined[name] {
				pairs = append(pairs, [2]string{name, value})
			}
		} else {
			pairs = append(pairs, [2]string{name, strings.Join(combined[name], ", ")})
		}
	}
	return pairs
}

func sdkStreamState(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	if raw == nil {
		return map[string]any{"kind": "undefined"}
	}
	raw = bytes.TrimSpace(raw)
	kind := "number"
	switch raw[0] {
	case 'n':
		kind = "null"
	case '"':
		kind = "string"
	case 't', 'f':
		kind = "boolean"
	case '{', '[':
		kind = "object"
	}
	return map[string]any{"kind": kind, "source": sdkStreamStringify(t, raw)}
}

func sdkStreamDeepErrorNumbers(t *testing.T, raw json.RawMessage) map[string]sdkStreamNumber {
	t.Helper()
	// The source profile has exactly one future field containing a 10001-deep
	// singleton zero. Compress only that explicit shape for shallow numeric
	// observation, then expand its path. Keep the original full payload for
	// stringify/validation; never use the compressed object in SDK behavior.
	deep := strings.Repeat("[", 10001) + "0" + strings.Repeat("]", 10001)
	if bytes.Count(raw, []byte(deep)) != 1 {
		t.Fatal("deep error fixture lost its frozen singleton raw shape")
	}
	compressed := bytes.Replace(raw, []byte(deep), []byte("0"), 1)
	result := sdkStreamNumberLeaves(t, compressed)
	leaf, ok := result[`["future"]`]
	if !ok || leaf.Value != "0" || leaf.NegativeZero {
		t.Fatal("deep error fixture requires the future singleton zero")
	}
	delete(result, `["future"]`)
	path := `["future",` + strings.Repeat("0,", 10000) + "0]"
	result[path] = leaf
	return result
}

func sdkStreamErrorObservation(t *testing.T, failure error, deepNumbers string) map[string]any {
	t.Helper()
	var err *SDKStreamError
	if !errors.As(failure, &err) {
		t.Fatalf("SDK stream returned a non-SDK error: %T %v", failure, failure)
	}
	status, requestID := sdkStreamState(t, nil), sdkStreamState(t, nil)
	if err.Status != nil {
		status = sdkStreamState(t, json.RawMessage(strconv.Itoa(*err.Status)))
	}
	var headers any
	if err.Headers != nil {
		headers = sdkStreamHeaders(err.Headers)
		if err.RequestID == nil {
			requestID = sdkStreamState(t, json.RawMessage("null"))
		} else {
			quoted, _ := json.Marshal(*err.RequestID)
			requestID = sdkStreamState(t, quoted)
		}
	}
	payload := sdkStreamState(t, err.Payload)
	if err.Payload != nil {
		if deepNumbers == "future" {
			payload["numberLeaves"] = sdkStreamDeepErrorNumbers(t, err.Payload)
		} else {
			payload["numberLeaves"] = sdkStreamNumberLeaves(t, err.Payload)
		}
	}
	message := err.MessageJSON
	if message == nil {
		message, _ = json.Marshal(err.Message)
	}
	return map[string]any{"kind": err.Kind, "class": err.Class, "name": err.Name, "message": message, "status": status, "requestID": requestID, "payload": payload, "headers": headers}
}

func sdkStreamRecord(t *testing.T, input sdkStreamScenario) map[string]any {
	t.Helper()
	wire := make([]byte, len(input.WireBytes))
	for index, value := range input.WireBytes {
		if value < 0 || value > 255 {
			t.Fatal("SDK fixture wire bytes must be integers in [0,255]")
		}
		wire[index] = byte(value)
	}
	for _, length := range input.Chunks {
		if length <= 0 {
			t.Fatal("SDK fixture chunks must be positive")
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != "POST" || request.URL.Path != "/v1/messages" {
			http.Error(writer, "unexpected SDK stream path", http.StatusNotFound)
			return
		}
		_, _ = io.Copy(io.Discard, request.Body)
		writer.Header().Set("content-type", "text/event-stream")
		writer.Header().Set("date", "Thu, 01 Jan 1970 00:00:00 GMT")
		writer.Header().Set("content-length", strconv.Itoa(len(wire)))
		writer.Header().Set("connection", "keep-alive")
		for _, pair := range input.Headers {
			writer.Header().Add(pair[0], pair[1])
		}
		status := input.Status
		if status == 0 {
			status = http.StatusOK
		}
		connection, buffer, err := writer.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer connection.Close()
		_, _ = fmt.Fprintf(buffer, "HTTP/1.1 %d Fixture\r\n", status)
		_ = writer.Header().Write(buffer)
		_, _ = buffer.WriteString("\r\n")
		_ = buffer.Flush()
		for offset, count := 0, 0; offset < len(wire); count++ {
			length := len(wire)
			if len(input.Chunks) > 0 {
				length = input.Chunks[count%len(input.Chunks)]
			}
			end := min(offset+length, len(wire))
			_, _ = buffer.Write(wire[offset:end])
			_ = buffer.Flush()
			offset = end
		}
	}))
	defer server.Close()
	yields := []any{}
	diagnostics := []any{}
	observation := map[string]any{"yields": yields, "error": nil, "diagnostics": diagnostics, "headers": [][2]string{}, "requestID": nil}
	client := NewAnthropic(AnthropicConfig{APIKey: "offline-fixture-key", BaseURL: server.URL, Timeout: 5 * time.Second, HTTPClient: server.Client(), StreamDiagnostic: func(message string, value any) {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		diagnostics = append(diagnostics, map[string]any{"message": message, "valueSource": sdkStreamStringify(t, raw)})
	}})
	stream, failure := client.OpenMessagesStream(context.Background(), &MessagesRequest{Path: "/v1/messages", Body: []byte(`{"model":"sdk-stream-fixture","messages":[],"max_tokens":32,"stream":true}`)})
	if failure == nil {
		defer stream.Close()
		observation["headers"], observation["requestID"] = sdkStreamHeaders(stream.Headers), stream.RequestID
		for {
			raw, err := stream.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				failure = err
				break
			}
			if input.DeepArray {
				// This explicit source profile is a singleton zero nested 10001
				// times, beyond encoding/json's validator limit. Observe the raw
				// returned value without routing it back through that validator.
				want := strings.Repeat("[", 10001) + "0" + strings.Repeat("]", 10001)
				if string(raw) != want {
					t.Fatal("deep raw SDK value differs from its frozen singleton shape")
				}
				path := "[" + strings.Repeat("0,", 10000) + "0]"
				yields = append(yields, map[string]any{"source": string(raw), "numberLeaves": map[string]sdkStreamNumber{path: {Value: "0", NegativeZero: false}}})
			} else {
				yields = append(yields, map[string]any{"source": sdkStreamStringify(t, raw), "numberLeaves": sdkStreamNumberLeaves(t, raw)})
			}
		}
	}
	if failure != nil {
		observation["error"] = sdkStreamErrorObservation(t, failure, input.DeepErrorNumbers)
		var sdk *SDKStreamError
		if stream == nil && errors.As(failure, &sdk) && sdk.Headers != nil {
			observation["headers"], observation["requestID"] = sdkStreamHeaders(sdk.Headers), sdk.RequestID
		}
	}
	observation["yields"], observation["diagnostics"] = yields, diagnostics
	return observation
}

// encoding/json's ordinary string decoding replaces lone surrogates. Keep
// strings and property identity as UTF-16 so native golden checks cannot
// silently accept replacement characters in SDK diagnostics.
func sdkStreamComparable(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	raw = bytes.TrimSpace(raw)
	switch raw[0] {
	case '{':
		var object protocol.JSONObject
		if err := object.UnmarshalJSON(raw); err != nil {
			t.Fatal(err)
		}
		ordered, err := object.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		result := map[string]any{}
		decoder := json.NewDecoder(bytes.NewReader(ordered))
		_, _ = decoder.Token()
		for decoder.More() {
			start := decoder.InputOffset()
			_, _ = decoder.Token()
			key := ordered[start:decoder.InputOffset()]
			key = key[bytes.IndexByte(key, '"'):]
			var value json.RawMessage
			_ = decoder.Decode(&value)
			result[sdkStreamStringify(t, key)] = sdkStreamComparable(t, value)
		}
		return result
	case '[':
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			t.Fatal(err)
		}
		result := make([]any, len(values))
		for index, value := range values {
			result[index] = sdkStreamComparable(t, value)
		}
		return result
	case '"':
		units, err := protocol.DecodeJavaScriptJSONString(raw)
		if err != nil {
			t.Fatal(err)
		}
		return struct{ UTF16 []uint16 }{units}
	default:
		return string(raw)
	}
}

func TestSDKStreamNativeComparatorRetainsLoneUTF16(t *testing.T) {
	if reflect.DeepEqual(sdkStreamComparable(t, json.RawMessage(`{"message":"\ud83d"}`)), sdkStreamComparable(t, json.RawMessage(`{"message":"\ufffd"}`))) {
		t.Fatal("native source comparator hid an SDK diagnostic UTF-16 change")
	}
}

func TestSDKStreamFixture(t *testing.T) {
	if os.Getenv("CC_HAHA_SDK_STREAM_FIXTURE") != "1" {
		t.Skip("explicit offline parity entry only")
	}
	var input sdkStreamScenario
	raw, err := io.ReadAll(os.Stdin)
	if err != nil || json.Unmarshal(raw, &input) != nil || input.Home == "" || input.Home != os.Getenv("HOME") || filepath.Join(input.Home, ".claude") != os.Getenv("CLAUDE_CONFIG_DIR") {
		t.Fatal("SDK stream fixture requires disposable HOME/config")
	}
	home, err := filepath.EvalSymlinks(input.Home)
	if err != nil {
		t.Fatal(err)
	}
	allocation, err := filepath.EvalSymlinks(os.Getenv("CC_HAHA_FIXTURE_TEMP_ROOT"))
	if err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cwd, err = filepath.EvalSymlinks(cwd)
	if err != nil || cwd != home || !strings.HasPrefix(home, allocation+string(filepath.Separator)) {
		t.Fatal("SDK stream fixture requires private allocated current HOME")
	}
	result, err := json.Marshal(map[string]any{"observation": sdkStreamRecord(t, input)})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("SDK_STREAM_RESULT:%s\n", result)
}

func TestSDKStreamPinnedSourceContract(t *testing.T) {
	raw, err := os.ReadFile("testdata/sdk_stream_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		SDKVersion    string `json:"sdkVersion"`
		EngineVersion string `json:"engineVersion"`
		Cases         []struct {
			Scenario    sdkStreamScenario `json:"scenario"`
			Observation json.RawMessage   `json:"observation"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &contract); err != nil || contract.SDKVersion != "0.80.0" || contract.EngineVersion != "1.3.14" || len(contract.Cases) == 0 {
		t.Fatal("SDK source contract requires pinned actual oracle provenance")
	}
	for _, fixture := range contract.Cases {
		t.Run(fixture.Scenario.Name, func(t *testing.T) {
			actualJSON, err := json.Marshal(sdkStreamRecord(t, fixture.Scenario))
			if err != nil {
				t.Fatal(err)
			}
			actual := sdkStreamComparable(t, actualJSON)
			expected := sdkStreamComparable(t, fixture.Observation)
			if !reflect.DeepEqual(actual, expected) {
				t.Fatalf("SDK stream differs from actual pinned Source\nactual: %s\nsource: %s", actualJSON, fixture.Observation)
			}
		})
	}
}
