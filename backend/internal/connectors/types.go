// Package connectors implements the desktop connector lifecycle over the
// original state file and real adapter/plugin/runtime capabilities.
package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string                  { return e.Message }
func apiError(status int, message string) error { return &Error{status, message} }

type Installation struct {
	Directory        string            `json:"directory"`
	Command          string            `json:"command"`
	Args             []string          `json:"args"`
	Environment      map[string]string `json:"env"`
	EnvironmentOrder []string          `json:"-"`
}

func (i Installation) MarshalJSON() ([]byte, error) {
	env := protocol.NewJSONObject()
	keys := append([]string(nil), i.EnvironmentOrder...)
	if keys == nil {
		for k := range i.Environment {
			keys = append(keys, k)
		}
		sort.Strings(keys)
	}
	seen := map[string]bool{}
	for _, k := range keys {
		if v, ok := i.Environment[k]; ok && !seen[k] {
			env.Set(k, quote(v))
			seen[k] = true
		}
	}
	remaining := []string{}
	for k := range i.Environment {
		if !seen[k] {
			remaining = append(remaining, k)
		}
	}
	sort.Strings(remaining)
	for _, k := range remaining {
		env.Set(k, quote(i.Environment[k]))
	}
	return record(field("directory", i.Directory), field("command", i.Command), field("args", i.Args), field("env", env)).MarshalJSON()
}

func (i *Installation) UnmarshalJSON(source []byte) error {
	type plain Installation
	var parsed plain
	if err := json.Unmarshal(source, &parsed); err != nil {
		return err
	}
	*i = Installation(parsed)
	i.EnvironmentOrder = []string{}
	for _, p := range nested(nested(source).Get("env")).OwnProperties() {
		var key string
		if err := json.Unmarshal(p.NameJSON, &key); err != nil {
			return err
		}
		i.EnvironmentOrder = append(i.EnvironmentOrder, key)
	}
	return nil
}

type Check struct {
	Authenticated bool
	AccountLabel  *string
	ToolCount     *int
	// Keep the Source label's UTF-16 units for JSON. A native UTF-8 display
	// string cannot represent a lone unit or a pair cut by String.slice.
	AccountLabelJSON json.RawMessage
	Verification     string
}

type Progress func(phase string, authURL *string)

type Adapter interface {
	Prepare(context.Context, Progress) (Installation, error)
	Authenticate(context.Context, Installation, Progress) error
	Check(context.Context, Installation) (Check, error)
	Deactivate(context.Context) error
	Remove(context.Context, Installation) error
}

type ConfigurableAdapter interface {
	Configure(context.Context, map[string]string) error
}

type Bridge interface {
	Install(context.Context, protocol.JSONObject, Installation) error
	SetEnabled(context.Context, protocol.JSONObject, bool) error
	Remove(context.Context, protocol.JSONObject) error
	Ready(context.Context, protocol.JSONObject) (bool, error)
	Reload(context.Context, string, *protocol.JSONObject) error
}

type ActionOptions struct {
	Configuration                map[string]string `json:"configuration,omitempty"`
	AcknowledgeSharedCredentials bool              `json:"acknowledgeSharedCredentials,omitempty"`
	SessionID                    string            `json:"sessionId,omitempty"`
}

type Options struct {
	Root, Platform string
	Definitions    []protocol.JSONObject
	CreateAdapter  func(protocol.JSONObject, string) (Adapter, error)
	Bridge         Bridge
	Now            func() time.Time
	UUID           func() string
	// Persistence primitive is replaceable only to exercise storage failures.
	WriteState func(string, []byte) error
}

func raw(v any) json.RawMessage      { b, _ := json.Marshal(v); return b }
func quote(v string) json.RawMessage { return raw(v) }
func truthy(value json.RawMessage) bool {
	value = bytes.TrimSpace(value)
	if len(value) == 0 || bytes.Equal(value, []byte("null")) || bytes.Equal(value, []byte("false")) {
		return false
	}
	switch value[0] {
	case '"':
		units, err := protocol.DecodeJavaScriptJSONString(value)
		return err == nil && len(units) > 0
	case '[', '{', 't':
		return true
	default:
		number, _ := strconv.ParseFloat(string(value), 64)
		return number != 0 && !math.IsNaN(number)
	}
}

func nullish(value json.RawMessage) bool {
	return len(value) == 0 || bytes.Equal(bytes.TrimSpace(value), []byte("null"))
}

func equalStrings(left, right json.RawMessage) bool {
	a, aErr := protocol.DecodeJavaScriptJSONString(left)
	b, bErr := protocol.DecodeJavaScriptJSONString(right)
	return aErr == nil && bErr == nil && slicesEqualUnits(a, b)
}

func slicesEqualUnits(a, b []uint16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func jsSpace(c rune) bool {
	return c == '\t' || c == '\n' || c == '\v' || c == '\f' || c == '\r' || c == ' ' || c == 0xa0 || c == 0x1680 || c >= 0x2000 && c <= 0x200a || c == 0x2028 || c == 0x2029 || c == 0x202f || c == 0x205f || c == 0x3000 || c == 0xfeff
}

func trimJS(s string) string { return strings.TrimFunc(s, jsSpace) }
func nested(value json.RawMessage) protocol.JSONObject {
	var object protocol.JSONObject
	_ = object.UnmarshalJSON(value)
	return object
}

func field(name string, value any) protocol.JSONField {
	return protocol.JSONField{Name: name, Value: raw(value)}
}
func record(fields ...protocol.JSONField) protocol.JSONObject {
	return protocol.NewJSONObject(fields...)
}
func undefined(name string) protocol.JSONField { return protocol.JSONField{Name: name} }
