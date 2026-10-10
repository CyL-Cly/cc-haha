// Package sessioncollaboration owns the original desktop collaboration store,
// admission fences and evidence projections. Runtime callbacks bind the same
// live sessions that receive renderer input; they never run another model loop.
package sessioncollaboration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const ReadMaxPages = 8
const WaitMinMS = 10000
const WaitDefaultMS = 30000
const WaitMaxMS = 300000

type APIError struct {
	Status        int
	Code, Message string
}

func (e *APIError) Error() string   { return e.Message }
func bad(message string) error      { return &APIError{400, "BAD_REQUEST", message} }
func missing(message string) error  { return &APIError{404, "NOT_FOUND", message} }
func conflict(message string) error { return &APIError{409, "CONFLICT", message} }

type Files interface {
	ReadFile(string) ([]byte, error)
	MkdirAll(string, os.FileMode) error
	WriteFile(string, []byte, os.FileMode) error
	Rename(string, string) error
}
type OSFiles struct{}

func (OSFiles) ReadFile(p string) ([]byte, error)                 { return os.ReadFile(p) }
func (OSFiles) MkdirAll(p string, m os.FileMode) error            { return os.MkdirAll(p, m) }
func (OSFiles) WriteFile(p string, b []byte, m os.FileMode) error { return os.WriteFile(p, b, m) }
func (OSFiles) Rename(a, b string) error                          { return os.Rename(a, b) }

type Options struct {
	StatePath    string
	Files        Files
	Now          func() time.Time
	UUID         func() string
	List         func(context.Context, protocol.JSONObject) (protocol.JSONObject, error)
	Read         func(context.Context, string, protocol.JSONObject) (protocol.JSONObject, error)
	Exists       func(context.Context, string) (bool, error)
	Create       func(context.Context, string, protocol.JSONObject) (protocol.JSONObject, error)
	Titles       func(context.Context, []string) (protocol.JSONObject, error)
	RuntimeState func(string) string
	Start        func(context.Context, string, protocol.JSONObject) error
	Enqueue      func(context.Context, string, protocol.JSONObject) error
	Stop         func(context.Context, string) error
	BeginStop    func(context.Context, string) <-chan error
}

func uuid() string {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	s := hex.EncodeToString(b[:])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}
func raw(v any) json.RawMessage {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return b
}
func decode(v json.RawMessage, out any) error { return json.Unmarshal(v, out) }
func optionalStringEqual(a, b json.RawMessage) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return stringEqual(a, b)
}
func object(fields ...any) protocol.JSONObject {
	o := protocol.NewJSONObject()
	for i := 0; i < len(fields); i += 2 {
		o.Set(fields[i].(string), raw(fields[i+1]))
	}
	return o
}
func parsed(r json.RawMessage) protocol.JSONObject {
	var o protocol.JSONObject
	_ = o.UnmarshalJSON(r)
	return o
}
func clone(o protocol.JSONObject) protocol.JSONObject { b, _ := o.MarshalJSON(); return parsed(b) }
func list(r json.RawMessage) []protocol.JSONObject {
	var a []protocol.JSONObject
	_ = json.Unmarshal(r, &a)
	if a == nil {
		return []protocol.JSONObject{}
	}
	return a
}
func num(r json.RawMessage) float64 { var n float64; _ = json.Unmarshal(r, &n); return n }
func isTrue(r json.RawMessage) bool { return string(r) == "true" }
func truthy(r json.RawMessage) bool {
	if len(r) == 0 || string(r) == "null" || string(r) == "false" {
		return false
	}
	if r[0] == '"' {
		return stringLength(r) > 0
	}
	if r[0] == '{' || r[0] == '[' || string(r) == "true" {
		return true
	}
	n, e := strconv.ParseFloat(string(r), 64)
	return e == nil && n != 0
}
func trim(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		return r == '\uFEFF' || r == '\t' || r == '\n' || r == '\v' || r == '\f' || r == '\r' || r == ' ' || r == '\u00A0' || r == '\u1680' || r >= 0x2000 && r <= 0x200A || r == '\u2028' || r == '\u2029' || r == '\u202F' || r == '\u205F' || r == '\u3000'
	})
}
func stringLength(r json.RawMessage) int {
	u, _ := protocol.DecodeJavaScriptJSONString(r)
	return len(u)
}
func stringEqual(a, b json.RawMessage) bool {
	x, e := protocol.DecodeJavaScriptJSONString(a)
	if e != nil {
		return false
	}
	y, e := protocol.DecodeJavaScriptJSONString(b)
	if e != nil || len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}
func stringSlice(r json.RawMessage, from, to int) json.RawMessage {
	u, e := protocol.DecodeJavaScriptJSONString(r)
	if e != nil {
		return raw("")
	}
	if from < 0 {
		from = 0
	}
	if to > len(u) {
		to = len(u)
	}
	if from > to {
		from = to
	}
	return protocol.QuoteJavaScriptUTF16(u[from:to])
}
func stringify(o protocol.JSONObject) json.RawMessage { b, _ := o.MarshalJSON(); return raw(string(b)) }
func utf16JSONLength(o protocol.JSONObject) int       { return stringLength(stringify(o)) }
func copyRaw(o *protocol.JSONObject, name string, v json.RawMessage) {
	if v != nil {
		o.Set(name, append(json.RawMessage(nil), v...))
	}
}
func awaitContext(ctx context.Context, ready <-chan struct{}) error {
	select {
	case <-ready:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
func errorCode(e error) string {
	if a, ok := e.(*APIError); ok {
		return a.Code
	}
	return ""
}
func ids(a []string) map[string]bool {
	if a == nil {
		return nil
	}
	m := map[string]bool{}
	for _, id := range a {
		m[id] = true
	}
	return m
}
func stateAllowed(s string) bool {
	switch s {
	case "queued", "running", "idle", "blocked", "completed", "failed", "stopped":
		return true
	}
	return false
}
func messageStatusAllowed(s string) bool {
	switch s {
	case "queued", "accepted", "consumed", "cancelled":
		return true
	}
	return false
}
func stringField(o protocol.JSONObject, k string) bool {
	r := o.Get(k)
	return len(r) > 0 && r[0] == '"'
}
func required(o Options) error {
	if o.List == nil || o.Read == nil || o.Exists == nil || o.Create == nil || o.Start == nil || o.Enqueue == nil || o.Stop == nil || o.BeginStop == nil {
		return fmt.Errorf("session collaboration requires actual session and runtime callbacks")
	}
	return nil
}
