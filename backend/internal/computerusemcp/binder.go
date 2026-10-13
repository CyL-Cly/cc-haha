// Package computerusemcp binds each platform's original MCP face to the shared
// SID-bound native facade. macOS uses the isolated JavaScript kernel; Windows
// exposes the original pixel tools through its native Python transport.
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

//go:embed assets/windows-tools.json
var windowsToolDefinitions []byte

type Binder struct {
	tools   *computeruse.NativeTools
	windows *computeruse.WindowsTools
	runtime *curepl.Runtime
	mu      sync.Mutex
	tail    chan struct{}
	epoch   uint64
	closed  bool
}

func NewWindows(tools *computeruse.WindowsTools) (*Binder, error) {
	if tools == nil {
		return nil, errors.New("Windows Computer Use MCP requires an actual SID-bound pixel facade")
	}
	return &Binder{windows: tools}, nil
}
func WindowsDefinitions(mode string) ([]protocol.JSONObject, error) {
	var definitions map[string][]protocol.JSONObject
	if err := json.Unmarshal(windowsToolDefinitions, &definitions); err != nil {
		return nil, err
	}
	tools, ok := definitions[mode]
	if !ok {
		return nil, errors.New("unsupported Windows Computer Use coordinate mode")
	}
	return tools, nil
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
	if b.windows != nil {
		if b.windows.Disabled(ctx) {
			return []protocol.JSONObject{}, nil
		}
		return WindowsDefinitions(b.windows.CoordinateMode())
	}
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
	if b.windows != nil {
		b.mu.Lock()
		closed := b.closed
		b.mu.Unlock()
		if closed {
			return resultError("Computer Use cancelled before dispatch.")
		}
		result, err := b.windows.Call(ctx, name, args)
		if err != nil {
			return resultError(err.Error())
		}
		// Telemetry and native execution metadata stop at the MCP boundary.
		return convertNativeResult(result)
	}
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
		return convertNativeResult(result)
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
func convertNativeResult(result computeruse.NativeToolResult) curepl.Result {
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
func (b *Binder) EndTurn(ctx context.Context) error {
	if b.windows != nil {
		return b.windows.EndTurn(ctx)
	}
	return b.tools.EndTurn(ctx)
}
func (b *Binder) Close(ctx context.Context) error {
	b.mu.Lock()
	b.closed = true
	b.epoch++
	b.mu.Unlock()
	if b.windows != nil {
		return b.windows.Close(ctx)
	}
	b.runtime.Close()
	return b.tools.Close(ctx)
}
