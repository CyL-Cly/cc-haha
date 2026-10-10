// Package curepl implements the isolated Computer Use JavaScript language
// kernel. Agent decisions and native policy remain in the existing Go Harness
// and Computer Use dispatcher; no provider or filesystem enters this realm.
package curepl

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/javascriptparse"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/dop251/goja"
	gojaparser "github.com/dop251/goja/parser"
)

// These are pure guest-language functions copied from the original realm and
// native-app facade. They have no Node imports, module loader or host objects.
// Production embeds them in the Go executable; Bun is only a Source oracle.
//
//go:embed assets/realm.js
var realmSource string

//go:embed assets/api.js
var apiSource string

const MaxCodeBytes = 256 * 1024
const MaxOutputBytes = 16 * 1024 * 1024
const MaxActions = 256

type workerMessage struct {
	Type      string          `json:"type"`
	CellID    int64           `json:"cellId,omitempty"`
	RequestID int64           `json:"requestId,omitempty"`
	Nonce     int64           `json:"nonce,omitempty"`
	Code      json.RawMessage `json:"code,omitempty"`
	Bootstrap string          `json:"bootstrap,omitempty"`
	Name      string          `json:"name,omitempty"`
	Args      json.RawMessage `json:"args,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
}

type guestTimer struct {
	deadline time.Time
	delay    time.Duration
	interval bool
}
type workerCell struct {
	id        int64
	accepting bool
	finished  bool
	pending   map[int64]bool
	timers    map[int64]guestTimer
	promise   *goja.Promise
	err       string
	bootstrap bool
}
type worker struct {
	vm          *goja.Runtime
	realm       *goja.Object
	compileCode goja.Callable
	parser      *javascriptparse.Parser
	owner       *cooperative.Owner
	bindings    []Binding
	active      *workerCell
	executing   *workerCell
	contextCell *workerCell
	initialized bool
	rejected    map[*goja.Promise]bool
	send        func(workerMessage) error
	outputError error
}

// Goja's promise reaction context prevents a detached continuation from a prior
// cell being attributed to whichever cell happens to execute next.
func (w *worker) Grab() interface{} { return w.executing }
func (w *worker) Resumed(value interface{}) {
	w.contextCell = w.executing
	w.executing, _ = value.(*workerCell)
}
func (w *worker) Exited() { w.executing = w.contextCell; w.contextCell = nil }

func newWorker(send func(workerMessage) error) (*worker, error) {
	w := &worker{vm: goja.New(), owner: cooperative.New(), rejected: map[*goja.Promise]bool{}, send: send}
	w.vm.SetParserOptions(gojaparser.WithDisableSourceMaps)
	// Retain the built-in compiler solely in Go. Its string-value entry point
	// preserves UTF16 source code units; the guest's eval and constructors below
	// are blocked and cannot retrieve this private callable.
	w.compileCode, _ = goja.AssertFunction(w.vm.Get("eval"))
	if err := w.installJSONParser(); err != nil {
		w.owner.Close()
		return nil, err
	}
	w.vm.SetAsyncContextTracker(w)
	w.vm.SetPromiseRejectionTracker(func(p *goja.Promise, operation goja.PromiseRejectionOperation) {
		if operation == goja.PromiseRejectionReject {
			w.rejected[p] = true
		} else {
			delete(w.rejected, p)
		}
	})
	if err := w.owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		var err error
		w.parser, err = javascriptparse.New(scope, w.owner)
		return err
	}); err != nil {
		w.owner.Close()
		return nil, err
	}
	// No host value except this JSON string function enters the guest. Goja is
	// constructed without goja_nodejs or any reflection/filesystem registrations.
	if err := w.vm.Set("__go_cu_bridge", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) != 1 {
			return w.vm.ToValue("Invalid Computer Use bridge request")
		}
		var value protocol.JSONObject
		if err := value.UnmarshalJSON([]byte(call.Argument(0).String())); err != nil || !value.IsObject() {
			return w.vm.ToValue("Invalid Computer Use bridge request")
		}
		failure := w.bridge(value)
		if failure != "" {
			return w.vm.ToValue(failure)
		}
		return goja.Undefined()
	}); err != nil {
		w.owner.Close()
		return nil, err
	}
	value, err := w.vm.RunScript("computer-use-realm.js", realmSource)
	if err != nil {
		w.owner.Close()
		return nil, err
	}
	w.realm = value.ToObject(w.vm)
	// Match vm's strings:false boundary, including all indirect constructors.
	// The blocking function and thrown EvalError are themselves guest objects.
	_, err = w.vm.RunScript("computer-use-code-generation.js", `(() => {
	 const blocked = function(){ throw new EvalError('Code generation from strings disallowed for this context') };
	 for (const fn of [function(){}, async function(){}, function*(){}]) {
	   Object.defineProperty(Object.getPrototypeOf(fn), 'constructor', {value: blocked, writable:false, configurable:false});
	 }
	 Object.defineProperty(globalThis, 'Function', {value:blocked,writable:false,configurable:false});
	 Object.defineProperty(globalThis, 'eval', {value:blocked,writable:false,configurable:false});
	})()`)
	if err != nil {
		w.owner.Close()
		return nil, err
	}
	return w, nil
}

func (w *worker) publish(message workerMessage) {
	if w.outputError == nil {
		w.outputError = w.send(message)
	}
}
func integer(value json.RawMessage) (int64, bool) {
	var number float64
	if json.Unmarshal(value, &number) != nil || number < 0 || number > 9007199254740991 || float64(int64(number)) != number {
		return 0, false
	}
	return int64(number), true
}
func (w *worker) bridge(value protocol.JSONObject) string {
	cell := w.executing
	if cell == nil || cell.finished || cell != w.active {
		if value.String("type") == "emit" {
			return ""
		}
		return "This JavaScript cell has ended; await every Computer Use operation"
	}
	switch value.String("type") {
	case "emit":
		w.publish(workerMessage{Type: "emit", CellID: cell.id, Content: append(json.RawMessage(nil), value.Get("content")...)})
		return ""
	case "submitted_done":
		cell.accepting = false
		return ""
	case "timer_error":
		if cell.err == "" {
			cell.err = truncateError(value.String("message"))
			cell.accepting = false
		}
		return ""
	case "timer":
		if !cell.accepting {
			break
		}
		id, ok := integer(value.Get("id"))
		if !ok || id < 1 {
			return "Invalid JavaScript timer ID"
		}
		if value.String("action") == "clear" {
			delete(cell.timers, id)
			return ""
		}
		var interval bool
		delay, valid := integer(value.Get("delay"))
		if value.String("action") != "schedule" || !valid || delay > 2147483647 || json.Unmarshal(value.Get("interval"), &interval) != nil {
			return "Invalid JavaScript timer request"
		}
		if _, exists := cell.timers[id]; exists {
			return "Duplicate JavaScript timer ID"
		}
		if len(cell.timers) >= 1024 {
			return "Too many active JavaScript timers (maximum 1024)"
		}
		duration := time.Duration(delay) * time.Millisecond
		cell.timers[id] = guestTimer{time.Now().Add(duration), duration, interval}
		return ""
	case "invoke":
		if !cell.accepting {
			break
		}
		id, valid := integer(value.Get("requestId"))
		args := protocol.NewJSONObject()
		argErr := args.UnmarshalJSON(value.Get("args"))
		_, nameErr := protocol.DecodeJavaScriptJSONString(value.Get("name"))
		if !valid || nameErr != nil || argErr != nil || !args.IsObject() {
			return "Invalid Computer Use bridge request"
		}
		if cell.pending[id] {
			return "Duplicate Computer Use bridge request"
		}
		cell.pending[id] = true
		w.publish(workerMessage{Type: "invoke", CellID: cell.id, RequestID: id, Name: value.String("name"), Args: append(json.RawMessage(nil), value.Get("args")...)})
		return ""
	}
	return "Submitted JavaScript has completed; await every Computer Use operation"
}

func truncateError(text string) string {
	units := utf16.Encode([]rune(text))
	if len(units) > 8000 {
		units = units[:8000]
	}
	return string(utf16.Decode(units))
}
func (w *worker) errorMessage(value goja.Value) string {
	message := "JavaScript execution failed"
	w.vm.Try(func() {
		if obj, ok := value.(*goja.Object); ok {
			if v := obj.Get("message"); v != nil && !goja.IsUndefined(v) && !goja.IsNull(v) {
				message = v.String()
				return
			}
		}
		message = value.String()
	})
	return truncateError(message)
}
func (w *worker) guest(name string, arguments ...goja.Value) (goja.Value, error) {
	fn, ok := goja.AssertFunction(w.realm.Get(name))
	if !ok {
		return nil, errors.New("Missing Computer Use realm operation " + name)
	}
	return fn(w.realm, arguments...)
}
func (w *worker) compile(ctx context.Context, code []uint16) (CompiledCell, error) {
	var parsed javascriptparse.Result
	err := w.owner.Run(ctx, func(scope *cooperative.Scope) error {
		var err error
		parsed, err = w.parser.ParseModule(scope, code)
		return err
	})
	if err != nil {
		return CompiledCell{}, err
	}
	if !parsed.Accepted {
		return CompiledCell{}, errors.New(string(utf16.Decode(parsed.Failure.Message)))
	}
	return CompileAST(code, parsed.AST, w.bindings, CompilerOptions{})
}
func (w *worker) startCell(ctx context.Context, id int64, code []uint16, bootstrap bool) error {
	if w.active != nil {
		w.publish(workerMessage{Type: "done", CellID: id, Error: "A JavaScript cell is already running"})
		return nil
	}
	cell := &workerCell{id: id, accepting: true, pending: map[int64]bool{}, timers: map[int64]guestTimer{}, bootstrap: bootstrap}
	w.active = cell
	w.executing = cell
	defer func() { w.executing = nil }()
	if _, err := w.guest("begin"); err != nil {
		return err
	}
	compiled, err := w.compile(ctx, code)
	if err != nil {
		cell.err = truncateError(err.Error())
		cell.accepting = false
		return w.finish()
	}
	for _, warning := range compiled.Warnings {
		raw, _ := json.Marshal(map[string]string{"type": "text", "text": "Warning: " + warning})
		w.publish(workerMessage{Type: "emit", CellID: id, Content: raw})
	}
	value, err := w.compileCode(goja.Undefined(), goja.StringFromUTF16(compiled.Source))
	if err != nil {
		var exception *goja.Exception
		if errors.As(err, &exception) {
			cell.err = w.errorMessage(exception.Value())
		} else {
			cell.err = truncateError(err.Error())
		}
		cell.accepting = false
		return w.finish()
	}
	value, err = w.guest("execute", value)
	if err != nil {
		cell.err = truncateError(err.Error())
		cell.accepting = false
		return w.finish()
	}
	promise, ok := value.Export().(*goja.Promise)
	if !ok {
		return errors.New("Computer Use cell did not return a promise")
	}
	cell.promise = promise
	// Observe only the submitted promise, preserving detection of other ignored
	// native failures and detached promise rejections at the task checkpoint.
	observe, err := w.vm.RunString(`p => p.catch(() => {})`)
	if err != nil {
		return err
	}
	fn, _ := goja.AssertFunction(observe)
	if _, err = fn(goja.Undefined(), value); err != nil {
		return err
	}
	return w.finish()
}

func (w *worker) finish() error {
	cell := w.active
	if cell == nil {
		return w.outputError
	}
	succeeded := false
	if cell.err == "" && cell.promise != nil {
		if cell.promise.State() == goja.PromiseStatePending {
			return w.outputError
		}
		succeeded = cell.promise.State() == goja.PromiseStateFulfilled
		if !succeeded {
			cell.err = w.errorMessage(cell.promise.Result())
		}
	}
	cell.accepting = false
	clear(cell.timers)
	if _, err := w.guest("clearTimers"); err != nil {
		return err
	}
	if len(cell.pending) > 0 {
		return w.outputError
	}
	// Host task checkpoint after the submitted promise and all existing RPCs.
	// Source stops the kernel on any detached unhandled asynchronous exception.
	if len(w.rejected) > 0 {
		return errors.New("Computer Use JavaScript worker stopped after an unhandled asynchronous error.")
	}
	background, err := w.guest("unobservedError")
	if err != nil {
		return err
	}
	if cell.err == "" && background != nil && !goja.IsUndefined(background) {
		cell.err = "An unawaited Computer Use operation failed: " + background.String() + ". Observe the current state before continuing; do not replay prior actions."
	}
	bindings, err := w.guest("commit", w.vm.ToValue(succeeded))
	if err != nil {
		return err
	}
	if err = json.Unmarshal([]byte(bindings.String()), &w.bindings); err != nil {
		return err
	}
	cell.finished = true
	w.active = nil
	if cell.bootstrap {
		if cell.err != "" {
			return errors.New(cell.err)
		}
		w.initialized = true
		w.publish(workerMessage{Type: "ready"})
	} else {
		w.publish(workerMessage{Type: "done", CellID: cell.id, Error: cell.err})
	}
	return w.outputError
}

func (w *worker) receive(ctx context.Context, message workerMessage) error {
	switch message.Type {
	case "ping":
		w.publish(workerMessage{Type: "pong", Nonce: message.Nonce})
		return w.outputError
	case "init":
		if w.initialized || w.active != nil {
			return errors.New("Computer Use JavaScript worker is already initialized")
		}
		return w.startCell(ctx, 0, utf16.Encode([]rune(message.Bootstrap)), true)
	case "response":
		cell := w.active
		if cell == nil || cell.id != message.CellID || !cell.pending[message.RequestID] {
			return nil
		}
		delete(cell.pending, message.RequestID)
		data, err := json.Marshal(message)
		if err != nil {
			return err
		}
		w.executing = cell
		_, err = w.guest("response", w.vm.ToValue(string(data)))
		w.executing = nil
		if err != nil {
			return err
		}
		return w.finish()
	case "run":
		if !w.initialized {
			w.publish(workerMessage{Type: "done", CellID: message.CellID, Error: "Computer Use JavaScript worker is not initialized"})
			return w.outputError
		}
		code, err := protocol.DecodeJavaScriptJSONString(message.Code)
		if err != nil {
			return err
		}
		return w.startCell(ctx, message.CellID, code, false)
	default:
		return errors.New("Invalid Computer Use worker protocol")
	}
}

func (w *worker) fireTimers() error {
	cell := w.active
	if cell == nil || !cell.accepting {
		return nil
	}
	now := time.Now()
	ids := make([]int64, 0, len(cell.timers))
	for id := range cell.timers {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := cell.timers[ids[i]], cell.timers[ids[j]]
		if a.deadline.Equal(b.deadline) {
			return ids[i] < ids[j]
		}
		return a.deadline.Before(b.deadline)
	})
	for _, id := range ids {
		timer, exists := cell.timers[id]
		if !exists {
			continue
		}
		if now.Before(timer.deadline) {
			continue
		}
		if timer.interval {
			timer.deadline = now.Add(timer.delay)
			cell.timers[id] = timer
		} else {
			delete(cell.timers, id)
		}
		w.executing = cell
		_, err := w.guest("fireTimer", w.vm.ToValue(id))
		w.executing = nil
		if err != nil {
			cell.err = truncateError(err.Error())
			cell.accepting = false
		}
		if err = w.finish(); err != nil {
			return err
		}
		if w.active != cell || !cell.accepting {
			break
		}
	}
	return nil
}

// RunWorker reads only NDJSON. It deliberately runs before CLI/bootstrap imports
// can read any provider configuration, user home, startup hook or Keychain.
func RunWorker(ctx context.Context, input io.Reader, output io.Writer) error {
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	w, err := newWorker(func(message workerMessage) error { return encoder.Encode(message) })
	if err != nil {
		return err
	}
	defer w.owner.Close()
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-ctx.Done():
			w.vm.Interrupt(context.Cause(ctx))
		case <-finished:
		}
	}()
	lines := make(chan []byte)
	failures := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(input)
		scanner.Buffer(make([]byte, 4096), MaxOutputBytes)
		for scanner.Scan() {
			line := append([]byte(nil), scanner.Bytes()...)
			select {
			case lines <- line:
			case <-finished:
				return
			}
		}
		failures <- scanner.Err()
	}()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case err := <-failures:
			return err
		case line := <-lines:
			var message workerMessage
			if err = json.Unmarshal(line, &message); err != nil {
				return fmt.Errorf("Invalid Computer Use worker JSON: %w", err)
			}
			if err = w.receive(ctx, message); err != nil {
				return err
			}
		case <-tick.C:
			if err = w.fireTimers(); err != nil {
				return err
			}
		}
	}
}

// Used by the Go parent, never supplied through provider/environment settings.
func BootstrapSource() string { return apiSource }
