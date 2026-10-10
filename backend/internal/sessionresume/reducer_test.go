package sessionresume

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

type nativeStep struct {
	Operation string   `json:"operation"`
	Sources   []string `json:"sources"`
	Bytes     string   `json:"bytes"`
}
type nativeContract struct {
	Name, Cwd, ProcessCwd string
	Classifier            bool
	Steps                 []nativeStep
	InitialBytes          string
	Expected              []map[string]any
}

func mustNative(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func nativeSource(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	mustNative(t, err)
	raw, err = protocol.ParseAndStringifyJavaScriptJSON(raw)
	mustNative(t, err)
	return string(raw)
}
func nativeFailure(err error) map[string]any {
	var typed *Failure
	if errors.As(err, &typed) {
		return map[string]any{"name": typed.Name, "message": typed.MessageValue()}
	}
	return map[string]any{"name": "Error", "message": err.Error()}
}
func observeNative(t *testing.T, input nativeContract) []map[string]any {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if input.InitialBytes != "" {
		raw, err := hex.DecodeString(input.InitialBytes)
		mustNative(t, err)
		mustNative(t, os.WriteFile(path, raw, 0600))
	}
	observations := []map[string]any{}
	for _, step := range input.Steps {
		allocations, diagnostics, errorRows := []map[string]any{}, []map[string]any{}, []map[string]any{}
		calls := 0
		now := func() time.Time {
			allocations = append(allocations, map[string]any{"kind": "time", "value": "2023-11-14T22:13:20.000Z"})
			return time.UnixMilli(1700000000000)
		}
		factory, err := messages.NewFactory(func() string {
			calls++
			value := fmt.Sprintf("00000000-0000-4000-8000-%012d", calls)
			allocations = append(allocations, map[string]any{"kind": "uuid", "value": value})
			return value
		}, now)
		mustNative(t, err)
		capture := func(err error) {
			errorRows = append(errorRows, nativeFailure(err))
		}
		reducer, err := New(Options{Factory: factory, TranscriptClassifier: input.Classifier, RelativePath: func(path json.RawMessage) (json.RawMessage, error) {
			return RelativePath(input.Cwd, input.ProcessCwd, path)
		}, OnDiagnostic: func(event Diagnostic) {
			diagnostics = append(diagnostics, map[string]any{"kind": event.Kind, "name": event.Name, "fields": event.Fields})
		}, OnError: func(err error) { now(); capture(err) }})
		mustNative(t, err)
		loader, err := sessionhistory.New(sessionhistory.Options{Files: sessionstore.OSFileSystem{}, Stat: func(path string) (int64, error) {
			info, err := os.Stat(path)
			if err != nil {
				return 0, err
			}
			return info.Size(), nil
		}, ReadFile: func(path string) ([]byte, error) { return os.ReadFile(path) }, DisablePrecompactSkip: func() bool { return false }, LeafPrune: func() bool { return false }, ParseDate: sessionprojection.ParseDateUTC, Compare: sessionhistory.CompareStrings, OnDiagnostic: func(event sessionhistory.Diagnostic) {
			if event.Message != "" {
				now()
				capture(errors.New(event.Message))
			}
			if event.Code != "" {
				diagnostics = append(diagnostics, map[string]any{"kind": "event", "name": event.Code, "fields": event.Fields})
			}
		}})
		mustNative(t, err)
		history := []*Message{}
		var result any
		var reduced Result
		var operationErr error
		switch step.Operation {
		case "rewrite", "append":
			bytes, err := hex.DecodeString(step.Bytes)
			mustNative(t, err)
			if step.Operation == "rewrite" {
				operationErr = os.WriteFile(path, bytes, 0600)
			} else {
				file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
				operationErr = err
				if err == nil {
					_, operationErr = file.Write(bytes)
					mustNative(t, file.Close())
				}
			}
			result = map[string]any{"ok": true}
		case "reduce":
			raws := []json.RawMessage{}
			for _, raw := range step.Sources {
				raws = append(raws, json.RawMessage(raw))
			}
			history, operationErr = ParseMessages(raws)
			if operationErr == nil {
				reduced, operationErr = reducer.Deserialize(history)
				result = reduced
			}
		case "path":
			loaded, err := reducer.LoadPath(loader, path)
			operationErr = err
			history = loaded.Input
			if history == nil {
				history = []*Message{}
			}
			reduced = loaded.Result
			result = loaded
		default:
			operationErr = errors.New("Unknown session resume operation")
		}
		var errorValue any
		var resultValue any
		if operationErr != nil {
			capture(operationErr)
			errorValue = nativeFailure(operationErr)
		} else {
			resultValue = nativeSource(t, result)
		}
		references := []int{}
		var interruptionIndex any
		for index, message := range reduced.Messages {
			reference := -1
			for inputIndex, original := range history {
				if message == original {
					reference = inputIndex
					break
				}
			}
			references = append(references, reference)
			if reduced.TurnInterruptionState.Message == message {
				interruptionIndex = index
			}
		}
		permissions := []map[string]any{}
		for _, message := range history {
			if string(message.Field("type")) == `"user"` {
				var id, value any
				if raw := message.UUID(); raw != nil {
					mustNative(t, json.Unmarshal(raw, &id))
				}
				if raw := message.Field("permissionMode"); raw != nil {
					mustNative(t, json.Unmarshal(raw, &value))
				}
				permissions = append(permissions, map[string]any{"uuid": id, "own": message.HasOwnField("permissionMode"), "value": value})
			}
		}
		observations = append(observations, map[string]any{"resultSource": resultValue, "historyAfterSource": nativeSource(t, history), "references": references, "interruptionIndex": interruptionIndex, "permissionOwn": permissions, "allocations": allocations, "diagnostics": diagnostics, "errors": errorRows, "failure": errorValue})
	}

	return observations
}
func TestActualSourceOrderedReducerAndIndependentJSONLContracts(t *testing.T) {
	raw, err := os.ReadFile("testdata/source_contract.json")
	mustNative(t, err)
	var capture struct {
		Source    string
		Contracts []nativeContract
	}
	mustNative(t, json.Unmarshal(raw, &capture))
	if len(capture.Contracts) < 70 || capture.Source != "Bun1.3.14 actual loadMessagesFromJsonlPath + deserializeMessagesWithInterruptDetection" {
		t.Fatal("missing actual Source capture")
	}
	for _, contract := range capture.Contracts {
		t.Run(contract.Name, func(t *testing.T) {
			actual := observeNative(t, contract)
			var normalized []map[string]any
			mustNative(t, json.Unmarshal([]byte(nativeSource(t, actual)), &normalized))
			if !reflect.DeepEqual(normalized, contract.Expected) {
				t.Fatalf("Source contract differs\nexpected %s\nactual %s", nativeSource(t, contract.Expected), nativeSource(t, normalized))
			}
		})
	}
}

// Compare actual Source failure JSON before decoding into native strings, so
// unpaired UTF-16 units in diagnostic messages cannot be erased by the test.
func TestRawFailureJSONMatchesActualSourceUnits(t *testing.T) {
	raw, err := os.ReadFile("testdata/source_contract.json")
	mustNative(t, err)
	var capture struct {
		Contracts []struct {
			Name, Cwd, ProcessCwd string
			Classifier            bool
			Steps                 []nativeStep
			Expected              []struct {
				Failure     json.RawMessage
				Diagnostics []map[string]any
			}
		}
	}
	mustNative(t, json.Unmarshal(raw, &capture))
	failures := 0
	for _, contract := range capture.Contracts {
		for index, step := range contract.Steps {
			expected := contract.Expected[index].Failure
			if step.Operation != "reduce" || string(expected) == "null" {
				continue
			}
			failures++
			t.Run(contract.Name, func(t *testing.T) {
				history, err := ParseMessages(rawMessages(step.Sources))
				mustNative(t, err)
				options := testReducer(t).options
				options.TranscriptClassifier = contract.Classifier
				diagnostics := []map[string]any{}
				options.OnDiagnostic = func(event Diagnostic) {
					diagnostics = append(diagnostics, map[string]any{"kind": event.Kind, "name": event.Name, "fields": event.Fields})
				}
				options.OnError = func(err error) {
					if err.Error() == "" {
						t.Error("native diagnostic text lost")
					}
				}
				options.RelativePath = func(raw json.RawMessage) (json.RawMessage, error) {
					return RelativePath(contract.Cwd, contract.ProcessCwd, raw)
				}
				reducer, err := New(options)
				mustNative(t, err)
				_, err = reducer.Deserialize(history)
				if err == nil {
					t.Fatal("actual Source failure vanished")
				}
				var observedDiagnostics []map[string]any
				mustNative(t, json.Unmarshal([]byte(nativeSource(t, diagnostics)), &observedDiagnostics))
				if !reflect.DeepEqual(observedDiagnostics, contract.Expected[index].Diagnostics) {
					t.Fatal("Source diagnostic event drift before failure")
				}
				actual, marshalErr := json.Marshal(err)
				mustNative(t, marshalErr)
				actual, marshalErr = protocol.ParseAndStringifyJavaScriptJSON(actual)
				mustNative(t, marshalErr)
				expected, marshalErr = protocol.ParseAndStringifyJavaScriptJSON(expected)
				mustNative(t, marshalErr)
				if string(actual) != string(expected) {
					t.Fatalf("raw Source diagnostic units differ\nexpected %s\nactual %s", expected, actual)
				}
			})
		}
	}
	if failures < 20 {
		t.Fatal("missing actual raw failure profiles")
	}
}
func rawMessages(values []string) []json.RawMessage {
	raws := make([]json.RawMessage, len(values))
	for index, raw := range values {
		raws[index] = json.RawMessage(raw)
	}
	return raws
}
