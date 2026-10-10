// Package computerusemcp binds the original two macOS MCP entry points to the
// guarded native tool facade and isolated Go JavaScript kernel. It never calls
// native helper actions directly or creates a second Agent/model consumer.
package computerusemcp

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"math"
	"sync"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/computeruse"
	"github.com/NanmiCoder/cc-haha/backend/internal/curepl"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

//go:embed assets/tools.json
var toolDefinitions []byte

type Binder struct {
	tools   *computeruse.NativeTools
	runtime *curepl.Runtime
	mu      sync.Mutex
	tail    chan struct{}
	epoch   uint64
	closed  bool
}

func New(tools *computeruse.NativeTools, kernel *curepl.Runtime) (*Binder, error) {
	if tools == nil || kernel == nil {
		return nil, errors.New("Computer Use MCP requires an actual SID-bound native facade and isolated kernel")
	}
	ready := make(chan struct{})
	close(ready)
	return &Binder{tools: tools, runtime: kernel, tail: ready}, nil
}
func (b *Binder) Tools(ctx context.Context) ([]protocol.JSONObject, error) {
	if b.tools.Disabled(ctx) {
		return []protocol.JSONObject{}, nil
	}
	var tools []protocol.JSONObject
	err := json.Unmarshal(toolDefinitions, &tools)
	return tools, err
}
func resultError(text string) curepl.Result {
	return curepl.Result{IsError: true, Content: []curepl.Content{{Type: "text", Text: text}}}
}
func input(raw json.RawMessage) (protocol.JSONObject, bool) {
	var value protocol.JSONObject
	err := value.UnmarshalJSON(raw)
	return value, err == nil && value.IsObject()
}
func (b *Binder) Call(ctx context.Context, name string, args json.RawMessage) curepl.Result {
	if name == "js_reset" {
		value, valid := input(args)
		if !valid || len(value.OwnProperties()) != 0 {
			return resultError("js_reset takes an empty object.")
		}
		b.mu.Lock()
		b.epoch++
		b.mu.Unlock()
		b.runtime.Reset()
		return curepl.Result{Content: []curepl.Content{{Type: "text", Text: "Computer Use JavaScript reset. Select an app again to continue."}}}
	}
	b.mu.Lock()
	before := b.tail
	next := make(chan struct{})
	epoch := b.epoch
	b.tail = next
	b.mu.Unlock()
	<-before
	defer close(next)
	b.mu.Lock()
	stale, closed := epoch != b.epoch, b.closed
	b.mu.Unlock()
	if stale {
		return resultError("Computer Use JavaScript was reset before this queued cell started.")
	}
	if closed || ctx.Err() != nil || b.tools.Disabled(ctx) {
		b.runtime.Reset()
		return resultError("Computer Use JavaScript is disabled or cancelled.")
	}
	if name != "js" {
		result, err := b.tools.Call(ctx, name, args)
		if err != nil {
			return resultError(err.Error())
		}
		raw, err := json.Marshal(result)
		if err != nil {
			return resultError(err.Error())
		}
		var output curepl.Result
		if err = json.Unmarshal(raw, &output); err != nil {
			return resultError(err.Error())
		}
		return output
	}
	value, valid := input(args)
	if !valid {
		return resultError("js requires an object with code.")
	}
	code, err := protocol.DecodeJavaScriptJSONString(value.Get("code"))
	valid = err == nil && len([]byte(string(utf16.Decode(code)))) <= curepl.MaxCodeBytes
	for _, property := range value.OwnProperties() {
		var key string
		_ = json.Unmarshal(property.NameJSON, &key)
		if key != "code" && key != "title" && key != "timeout_ms" {
			valid = false
		}
	}
	if title := value.Get("title"); title != nil {
		if _, err := protocol.DecodeJavaScriptJSONString(title); err != nil {
			valid = false
		}
	}
	timeout := float64(30000)
	if raw := value.Get("timeout_ms"); raw != nil && string(raw) != "null" {
		if json.Unmarshal(raw, &timeout) != nil {
			valid = false
		}
	}
	if math.IsNaN(timeout) || math.IsInf(timeout, 0) || timeout != math.Trunc(timeout) || timeout < 1 || timeout > 60000 {
		valid = false
	}
	if !valid {
		return resultError("js requires code (up to 256 KiB), an optional title, and timeout_ms between 1 and 60000.")
	}
	return b.runtime.RunUTF16(ctx, code, int(timeout), func(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
		result, err := b.tools.Call(ctx, name, args)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	}, func() bool { return b.tools.Disabled(ctx) })
}
func (b *Binder) EndTurn(ctx context.Context) error { return b.tools.EndTurn(ctx) }
func (b *Binder) Close(ctx context.Context) error {
	b.mu.Lock()
	b.closed = true
	b.epoch++
	b.mu.Unlock()
	b.runtime.Close()
	return b.tools.Close(ctx)
}
