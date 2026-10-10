package apipresentation

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/retrypolicy"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

type originalError struct {
	Kind      string          `json:"kind"`
	Message   json.RawMessage `json:"message"`
	Code      json.RawMessage `json:"code"`
	Cause     *originalError  `json:"cause"`
	SelfCause bool            `json:"selfCause"`
	Status    *int            `json:"status"`
}
type originalCase struct {
	Name           string         `json:"name"`
	Error          *originalError `json:"error"`
	FactoryOptions storage.Object `json:"factoryOptions"`
	PriorUUIDs     int            `json:"priorUUIDs"`
	WrapRetry      bool           `json:"wrapRetry"`
	Supported      *bool          `json:"supported"`
}
type fixtureOriginal struct {
	Home  string            `json:"home"`
	Env   map[string]string `json:"env"`
	Cases []originalCase    `json:"cases"`
}
type causeRecord struct {
	spec  *originalError
	cause error
}

func validateOriginalError(spec *originalError, depth int) error {
	if spec == nil || depth > 16 {
		return errors.New("terminal creation requires neutral original Error fields")
	}
	switch spec.Kind {
	case "error", "plain":
	case "connection", "timeout", "abort", "api":
		if spec.Code != nil || spec.SelfCause || spec.Kind != "api" && spec.Status != nil {
			return errors.New("terminal creation rejects unported original Error fields")
		}
	default:
		return errors.New("terminal creation requires neutral original Error fields")
	}
	if spec.Message != nil {
		if _, err := protocol.DecodeJavaScriptJSONString(spec.Message); err != nil {
			return errors.New("terminal creation requires original Error string message")
		}
	}
	if spec.Cause != nil {
		return validateOriginalError(spec.Cause, depth+1)
	}
	return nil
}

func (record *causeRecord) Error() string                       { return "original cause" }
func (record *causeRecord) Unwrap() error                       { return record.cause }
func (record *causeRecord) SourceErrorInstance() bool           { return record.spec.Kind != "plain" }
func (record *causeRecord) ConnectionCodeJSON() json.RawMessage { return record.spec.Code }
func fromOriginal(spec *originalError) error {
	if spec == nil {
		return nil
	}
	if spec.Kind == "error" || spec.Kind == "plain" {
		record := &causeRecord{spec: spec}
		if spec.SelfCause {
			record.cause = record
		} else {
			record.cause = fromOriginal(spec.Cause)
		}
		return record
	}
	class, kind, message := "APIConnectionError", "request_connection", "Connection error."
	switch spec.Kind {
	case "timeout":
		class, kind, message = "APIConnectionTimeoutError", "request_timeout", "Request timed out."
	case "abort":
		class, kind, message = "APIUserAbortError", "request_abort", "Request was aborted."
	case "api":
		class, kind, message = "APIError", "http_api", "Original API error"
	}
	raw := spec.Message
	if raw == nil {
		raw, _ = json.Marshal(message)
	}
	units, _ := protocol.DecodeJavaScriptJSONString(raw)
	return &provider.SDKStreamError{Class: class, Kind: kind, Name: "Error", Message: string(utf16.Decode(units)), MessageJSON: raw, Status: spec.Status, Cause: fromOriginal(spec.Cause)}
}

type fixtureRow struct {
	Name        string   `json:"name"`
	Result      string   `json:"result,omitempty"`
	Allocations []string `json:"allocations"`
	UUIDCalls   int      `json:"uuidCalls"`
	Capability  bool     `json:"capability,omitempty"`
}

func runOriginal(env map[string]string, item originalCase) (fixtureRow, error) {
	row := fixtureRow{Name: item.Name, Allocations: []string{}, UUIDCalls: item.PriorUUIDs}
	factory, _ := messages.NewFactory(func() string {
		row.UUIDCalls++
		row.Allocations = append(row.Allocations, fmt.Sprint("uuid:", row.UUIDCalls))
		return fmt.Sprint("terminal-id-", row.UUIDCalls)
	}, func() time.Time {
		row.Allocations = append(row.Allocations, "date")
		return time.UnixMilli(1700000000000)
	})
	domain, err := DomainFromOriginalEnv(env)
	if err != nil {
		return row, err
	}
	var raw []byte
	if item.FactoryOptions != nil {
		record, failure := factory.CreateOrderedAssistantAPIError(item.FactoryOptions)
		if failure != nil {
			return row, failure
		}
		raw, err = record.MarshalJSON()
	} else {
		failure := fromOriginal(item.Error)
		if item.WrapRetry {
			failure = &retrypolicy.CannotRetryError{Original: failure, Context: retrypolicy.RetryContext{Model: "deepseek-flash"}}
		}
		envelope, failure := CreationFailure(failure, domain, factory)
		if failure != nil {
			var capability *CapabilityError
			if errors.As(failure, &capability) {
				row.Capability = true
				return row, nil
			}
			return row, failure
		}
		raw, err = envelope.MarshalJSON()
	}
	if err != nil {
		return row, err
	}
	raw, err = protocol.ParseAndStringifyJavaScriptJSON(raw)
	row.Result = string(raw)
	return row, err
}
func TestTerminalCreationFixture(t *testing.T) {
	if os.Getenv("CC_HAHA_TERMINAL_CREATION_FIXTURE") == "" {
		t.Skip("actual exported Source differential child")
	}
	var input fixtureOriginal
	decoder := json.NewDecoder(os.Stdin)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		t.Fatal(err)
	}
	home, err := filepath.EvalSymlinks(input.Home)
	root, rootErr := filepath.EvalSymlinks(os.Getenv("CC_HAHA_FIXTURE_TEMP_ROOT"))
	cwd, _ := os.Getwd()
	cwd, _ = filepath.EvalSymlinks(cwd)
	if err != nil || rootErr != nil || input.Home != os.Getenv("HOME") || input.Home != os.Getenv("USERPROFILE") || cwd != home || !strings.HasPrefix(home, root+string(filepath.Separator)) || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(input.Home, ".claude") || os.Getenv("NODE_ENV") != "production" || os.Getenv("BUN_OPTIONS") != "--no-env-file" || os.Getenv("CC_HAHA_TERMINAL_CREATION_FIXTURE") != "1" {
		t.Fatal("terminal creation requires disposable HOME/config")
	}
	for _, path := range []string{input.Home, filepath.Join(input.Home, ".claude")} {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			t.Fatal("terminal creation requires private directories")
		}
	}
	var rows []fixtureRow
	for _, item := range input.Cases {
		if item.PriorUUIDs < 0 || item.PriorUUIDs > 8 || item.Error == nil && item.FactoryOptions == nil || item.Error != nil && item.FactoryOptions != nil {
			t.Fatal("terminal creation requires original constructor/options")
		}
		if item.Error != nil {
			if err := validateOriginalError(item.Error, 0); err != nil {
				t.Fatal(err)
			}
		}
		row, err := runOriginal(input.Env, item)
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	raw, _ := json.Marshal(map[string]any{"rows": rows})
	fmt.Printf("TERMINAL_CREATION_RESULT:%s\n", raw)
}
