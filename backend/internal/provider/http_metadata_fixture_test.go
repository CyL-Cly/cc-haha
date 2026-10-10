package provider_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/retrypolicy"
)

type metadataOperation struct{ Kind, Name, Value string }
type metadataCase struct {
	Name, Kind                                      string
	Pairs                                           [][2]string
	Record                                          bool
	Operations, AfterErrorOperations                []metadataOperation
	Chunked, OmitConnection, EOF, Latin1HeaderBytes bool
	Connection, Version                             string
}
type metadataInput struct {
	Home  string
	Cases []metadataCase
}

const metadataPayload = `{"message":"metadata upstream","future":9007199254740993,"text":"<>&\ud800"}`

func metadataBody(item metadataCase) string {
	if item.Kind == "http" {
		return metadataPayload
	}
	if item.Kind == "sse" {
		return "event: error\ndata: " + metadataPayload + "\n\n"
	}
	return "event: completion\ndata: {\"completion\":\"metadata fixture\"}\n\n"
}
func metadataWirePairs(item metadataCase) [][2]string {
	pairs := append([][2]string{}, item.Pairs...)
	if item.Chunked {
		pairs = append(pairs, [2]string{"transfer-encoding", "chunked"})
	} else if !item.EOF {
		pairs = append(pairs, [2]string{"content-length", strconv.Itoa(len(metadataBody(item)))})
	}
	if !item.OmitConnection {
		value := item.Connection
		if value == "" {
			value = "close"
		}
		pairs = append(pairs, [2]string{"connection", value})
	}
	return pairs
}
func metadataHome(t *testing.T, home string) {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(home)
	root, rootErr := filepath.EvalSymlinks(os.Getenv("CC_HAHA_FIXTURE_TEMP_ROOT"))
	cwd, _ := os.Getwd()
	cwd, _ = filepath.EvalSymlinks(cwd)
	if err != nil || rootErr != nil || home != os.Getenv("HOME") || home != os.Getenv("USERPROFILE") || cwd != canonical || !strings.HasPrefix(canonical, root+string(filepath.Separator)) || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(home, ".claude") || os.Getenv("BUN_OPTIONS") != "--no-env-file" {
		t.Fatal("HTTP metadata fixture requires disposable HOME/config before execution")
	}
	for _, path := range []string{home, filepath.Join(home, ".claude")} {
		if info, err := os.Lstat(path); err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
			t.Fatal("HTTP metadata fixture requires disposable HOME/config before execution")
		}
	}
}
func metadataQuote(raw json.RawMessage) string {
	return string(protocol.StringifyValidatedJavaScriptJSON(raw))
}
func metadataState(raw json.RawMessage) map[string]string {
	if len(raw) == 0 {
		return map[string]string{"kind": "undefined"}
	}
	kind := "object"
	if string(raw) == "null" {
		kind = "null"
	} else if raw[0] == '"' {
		kind = "string"
	} else if raw[0] != '{' && raw[0] != '[' {
		kind = "number"
	}
	return map[string]string{"kind": kind, "source": metadataQuote(raw)}
}
func metadataHeaders(headers *provider.BunHeaders, pairs [][2]string) map[string]any {
	get := [][]any{}
	seen := map[string]bool{}
	for _, pair := range pairs {
		name := strings.ToLower(pair[0])
		if seen[name] {
			continue
		}
		seen[name] = true
		value, exists, err := headers.Get(name)
		if err != nil {
			panic(err)
		}
		var result any
		if exists {
			result = value
		}
		get = append(get, []any{name, result})
	}
	iterator := [][2]string{}
	for _, pair := range headers.Iterator() {
		iterator = append(iterator, [2]string{pair.Name, pair.Value})
	}
	wire, err := headers.MarshalJSON()
	if err != nil {
		panic(err)
	}
	keys := []string{}
	decoder := json.NewDecoder(strings.NewReader(string(wire)))
	_, _ = decoder.Token()
	for decoder.More() {
		name, _ := decoder.Token()
		keys = append(keys, name.(string))
		var value json.RawMessage
		_ = decoder.Decode(&value)
	}
	return map[string]any{"get": get, "iterator": iterator, "jsonSource": string(wire), "stringifySource": string(wire), "jsonKeys": keys, "setCookies": headers.SetCookies()}
}
func metadataOriginalPairs(item metadataCase) []provider.HeaderPair {
	pairs := item.Pairs
	if item.Record {
		object := protocol.NewJSONObject()
		for _, pair := range pairs {
			raw, _ := json.Marshal(pair[1])
			object.Set(pair[0], raw)
		}
		raw, _ := object.MarshalJSON()
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		_, _ = decoder.Token()
		pairs = nil
		for decoder.More() {
			name, _ := decoder.Token()
			var value string
			_ = decoder.Decode(&value)
			pairs = append(pairs, [2]string{name.(string), value})
		}
	}
	result := []provider.HeaderPair{}
	for _, pair := range pairs {
		result = append(result, provider.HeaderPair{Name: pair[0], Value: pair[1]})
	}
	return result
}

func metadataRecord(t *testing.T, cases []metadataCase) map[string]any {
	t.Helper()
	observations := []any{}
	ids, requests := 0, 0
	for _, item := range cases {
		var headers *provider.BunHeaders
		var initial, clone any
		var failure *provider.SDKStreamError
		values := []string{}
		pairs := item.Pairs
		if item.Kind == "constructed" {
			var err error
			headers, err = provider.NewConstructorHeaders(metadataOriginalPairs(item))
			if err != nil {
				t.Fatal(err)
			}
			initial = metadataHeaders(headers, pairs)
			for _, operation := range item.Operations {
				switch operation.Kind {
				case "delete":
					err = headers.Delete(operation.Name)
				case "set":
					err = headers.Set(operation.Name, operation.Value)
				case "append":
					err = headers.Append(operation.Name, operation.Value)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			clone = metadataHeaders(headers.Clone(), pairs)
			status := 500
			err = provider.NewSDKResponseError(&status, metadataPayload, headers, true)
			if !errors.As(err, &failure) {
				t.Fatal(err)
			}

		} else {
			pairs = metadataWirePairs(item)
			var count atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				_, _ = io.Copy(io.Discard, request.Body)
				count.Add(1)
				connection, buffer, err := writer.(http.Hijacker).Hijack()
				if err != nil {
					return
				}
				defer connection.Close()
				status := 200
				if item.Kind == "http" {
					status = 500
				}
				version := item.Version
				if version == "" {
					version = "HTTP/1.1"
				}
				fmt.Fprintf(buffer, "%s %d Fixture\r\n", version, status)
				for _, pair := range pairs {
					value := pair[1]
					if item.Latin1HeaderBytes {
						var raw []byte
						for _, character := range value {
							if character > 255 {
								t.Error("fixture wire value exceeds Latin1")
								return
							}
							raw = append(raw, byte(character))
						}
						value = string(raw)
					}
					fmt.Fprintf(buffer, "%s: %s\r\n", pair[0], value)
				}
				fmt.Fprint(buffer, "\r\n")
				body := metadataBody(item)
				if item.Chunked {
					fmt.Fprintf(buffer, "%x\r\n%s\r\n0\r\n\r\n", len(body), body)
				} else {
					fmt.Fprint(buffer, body)
				}
				_ = buffer.Flush()
			}))
			client := provider.NewAnthropic(provider.AnthropicConfig{APIKey: "metadata-offline-key", BaseURL: server.URL, Timeout: 3 * time.Second, HTTPExchange: &provider.RawHTTP1Exchange{}})
			stream, err := client.OpenMessagesStream(context.Background(), &provider.MessagesRequest{Path: "/v1/messages", Body: []byte(`{"model":"metadata-fixture","messages":[],"max_tokens":32,"stream":true}`)})
			if err == nil {
				headers = stream.OriginalHeaders
				for {
					value, nextErr := stream.Next()
					if nextErr != nil {
						err = nextErr
						break
					}
					values = append(values, metadataQuote(value))
				}
				_ = stream.Close()
			}
			if err != nil && !errors.Is(err, io.EOF) {
				if !errors.As(err, &failure) {
					t.Fatal(err)
				}
				headers = failure.OriginalHeaders
			}
			requests += int(count.Load())
			server.Close()
		}
		for _, operation := range item.AfterErrorOperations {
			var err error
			switch operation.Kind {
			case "delete":
				err = headers.Delete(operation.Name)
			case "set":
				err = headers.Set(operation.Name, operation.Value)
			case "append":
				err = headers.Append(operation.Name, operation.Value)
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		var observedError, notice any
		if failure != nil {
			status := json.RawMessage(nil)
			if failure.Status != nil {
				status = []byte(strconv.Itoa(*failure.Status))
			}
			requestID := json.RawMessage(`null`)
			if failure.RequestID != nil {
				requestID, _ = json.Marshal(*failure.RequestID)
			}
			observedError = map[string]any{"class": failure.Class, "name": failure.Name, "messageSource": string(failure.MessageJSON), "status": metadataState(status), "requestID": metadataState(requestID), "payload": metadataState(failure.Payload)}
			ids++
			record, err := (retrypolicy.RetryNotice{Type: "system", Subtype: "api_error", Level: "error", Error: failure, RetryInMillis: 500, RetryAttempt: 1, MaxRetries: 4, Timestamp: "2023-11-14T22:13:20.000Z", UUID: fmt.Sprintf("metadata-notice-%d", ids)}).Envelope()
			if err != nil {
				notice = "UNPORTED: " + err.Error()
			} else {
				raw, _ := record.MarshalJSON()
				notice = string(raw)
			}
		}
		var retryDelay any
		if failure != nil {
			var hint *string
			if value, exists, err := failure.HeaderValue("retry-after"); err != nil {
				t.Fatal(err)
			} else if exists {
				hint = &value
			}
			delay := retrypolicy.Delay(1, hint, 32000, func() float64 { return 0 })
			raw, _ := json.Marshal(delay)
			retryDelay = string(raw)
		}
		observations = append(observations, map[string]any{"name": item.Name, "headers": metadataHeaders(headers, pairs), "initial": initial, "clone": clone, "error": observedError, "noticeSource": notice, "values": values, "retryDelaySource": retryDelay})
	}
	return map[string]any{"observations": observations, "requestCount": requests, "ids": ids}
}
func TestHTTPMetadataFixture(t *testing.T) {
	if os.Getenv("CC_HAHA_HTTP_METADATA_FIXTURE") != "1" {
		t.Skip("private metadata fixture only")
	}
	var input metadataInput
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		t.Fatal(err)
	}
	metadataHome(t, input.Home)
	raw, err := json.Marshal(map[string]any{"observation": metadataRecord(t, input.Cases)})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("HTTP_METADATA_OBSERVATION " + string(raw))
}

// This expected trace was captured from Bun 1.3.14 and the pinned SDK, then
// independently self-replayed. It is never supplied to the production builder.
func TestHTTPMetadataActualSourceContract(t *testing.T) {
	raw, err := os.ReadFile("testdata/http_metadata_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		Cases       []metadataCase
		Observation map[string]any
	}
	if err := json.Unmarshal(raw, &contract); err != nil {
		t.Fatal(err)
	}
	if len(contract.Cases) != 249 {
		t.Fatalf("incomplete Source profiles: %d", len(contract.Cases))
	}
	actualRaw, err := json.Marshal(metadataRecord(t, contract.Cases))
	if err != nil {
		t.Fatal(err)
	}
	var actual map[string]any
	if err := json.Unmarshal(actualRaw, &actual); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, contract.Observation) {
		for index, expected := range contract.Observation["observations"].([]any) {
			if !reflect.DeepEqual(actual["observations"].([]any)[index], expected) {
				t.Fatalf("actual Source profile %s changed: got %s", contract.Cases[index].Name, actualRaw)
			}
		}
		t.Fatal("actual Source request/UUID side effects changed")
	}
}
