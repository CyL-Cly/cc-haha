package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/jsonschemavalidator"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/dop251/goja"
)

// AgentExecution is the real child Harness producer seam. Values crossing the
// language boundary are copied JSON, not Go objects or foreign owner scopes.
type AgentExecution struct {
	Prompt              string
	Options             protocol.JSONObject
	Index               int
	PhaseIndex          int
	PhaseTitle          string
	WorkflowName, RunID string
	OnAgentID           func(string) error
	OnProgress          func(AgentProgress) error
	Schema              *SchemaReference
}
type AgentOutcome struct {
	AgentID           string
	Value             json.RawMessage
	Tokens, ToolCalls float64
}
type ExecutionOptions struct {
	Program                    *goja.Program
	RunID, WorkflowName, Model string
	Args                       json.RawMessage
	SeedPhaseTitles            []string
	Now                        func() float64
	RunAgent                   func(context.Context, AgentExecution) (AgentOutcome, error)
	Emit                       func(protocol.JSONObject) error
	Concurrency                int
	BudgetTotal                *float64
	GetTurnSpent               func() float64
	Journal                    *Journal
	JournalSnapshot            *JournalSnapshot
	WorktreeUnavailableReason  func() (string, error)
	OnAgentController          func(string, context.CancelCauseFunc) error
	PrepareNested              func(context.Context, json.RawMessage) (PreparedScript, error)
	shared                     *runtimeShared
	nested                     bool
}
type ExecutionOutcome struct {
	Result         json.RawMessage
	AgentCount     int
	Logs, Failures []string
	DurationMillis float64
	Error          string
}
type runtimeReaction struct{ run func() error }
type execution struct {
	options                            ExecutionOptions
	vm                                 *goja.Runtime
	parse, stringify, coerce, describe goja.Callable
	argsStringify                      goja.Callable
	safeStringify                      goja.Callable
	schemaFactory                      *jsonschemavalidator.ActorFactory
	stringConstructor                  goja.Callable
	errorConstructor                   goja.Callable
	arrayCheck, cloneArray, allSettled goja.Callable
	ctx                                context.Context
	reactions                          chan runtimeReaction
	jobs                               sync.WaitGroup
	result                             ExecutionOutcome
	phases                             map[string]int
	phase                              string
	index                              int
	active                             int
	queue                              []func()
	timers                             map[int]*time.Timer
	timerNext                          int
	timerHandles                       map[*goja.Object]int
	cancel                             context.CancelFunc
	previousCacheKey                   json.RawMessage
	cacheExhausted, isolationReported  bool
	controllers                        map[string]context.CancelCauseFunc
}

// Execute runs only user-authored JavaScript in a dedicated native language
// actor. Workflow business, child creation and persistence stay Go producers.
// This actor is not a model Harness or a substitute Session.
func Execute(ctx context.Context, options ExecutionOptions) (ExecutionOutcome, error) {
	if options.Program == nil || options.RunID == "" || (options.WorkflowName == "" && !options.nested) || options.Now == nil || options.RunAgent == nil || options.Emit == nil || options.GetTurnSpent == nil {
		return ExecutionOutcome{}, &CapabilityError{"actual Workflow program/clock/Agent/event producers"}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	x := &execution{options: options, vm: goja.New(), ctx: ctx, cancel: cancel, reactions: make(chan runtimeReaction, 4096), phases: map[string]int{}, result: ExecutionOutcome{Logs: []string{}, Failures: []string{}}}
	x.previousCacheKey = quoted("")
	x.timers = map[int]*time.Timer{}
	x.timerHandles = map[*goja.Object]int{}
	x.controllers = map[string]context.CancelCauseFunc{}
	defer func() {
		// The language actor remains the only caller of the controller registry,
		// including when the outer run is canceled before a reaction is admitted.
		for key, controller := range x.controllers {
			controller(context.Cause(ctx))
			if options.OnAgentController != nil {
				_ = options.OnAgentController(key, nil)
			}
		}
	}()
	defer func() {
		for _, timer := range x.timers {
			timer.Stop()
		}
	}()
	if options.Concurrency < 1 {
		x.options.Concurrency = 2
	}
	if x.options.shared == nil {
		x.options.shared = &runtimeShared{limit: x.options.Concurrency}
	}
	start := options.Now()
	if e := x.initialize(); e != nil {
		return x.result, e
	}
	value, e := x.runProgram()
	if e != nil {
		x.result.Error = x.describeError(e)
		x.result.Result = []byte("null")
	} else {
		promise, ok := value.Export().(*goja.Promise)
		if !ok {
			return x.result, &CapabilityError{"Workflow async program promise"}
		}
		for promise.State() == goja.PromiseStatePending {
			select {
			case <-ctx.Done():
				x.result.Error = "Workflow aborted"
				x.result.Result = []byte("null")
				cancel()
				x.jobs.Wait()
				_ = x.flushJournal()
				x.result.AgentCount = x.options.shared.count()
				x.result.DurationMillis = options.Now() - start
				return x.result, nil
			case reaction := <-x.reactions:
				if e := reaction.run(); e != nil {
					x.result.Error = x.describeError(e)
					x.result.Result = []byte("null")
					cancel()
					x.jobs.Wait()
					_ = x.flushJournal()
					x.result.AgentCount = x.options.shared.count()
					x.result.DurationMillis = options.Now() - start
					return x.result, nil
				}
			}
		}
		if promise.State() == goja.PromiseStateRejected {
			x.result.Error = x.describeValue(promise.Result())
			x.result.Result = []byte("null")
		} else {
			x.result.Result, e = x.export(promise.Result(), true)
			if e != nil {
				x.result.Error = e.Error()
				x.result.Result = []byte("null")
			}
		}
	}
	cancel()
	x.jobs.Wait()
	if e := x.flushJournal(); e != nil && x.result.Error == "" {
		x.result.Error = e.Error()
		x.result.Result = []byte("null")
	}
	x.result.AgentCount = x.options.shared.count()
	x.result.DurationMillis = options.Now() - start
	return x.result, nil
}
func (x *execution) initialize() error {
	get := func(name, source string) (goja.Callable, error) {
		value, e := x.vm.RunScript(name, source)
		if e != nil {
			return nil, e
		}
		call, ok := goja.AssertFunction(value)
		if !ok {
			return nil, errors.New("Workflow language bridge function missing")
		}
		return call, nil
	}
	var e error
	x.schemaFactory, e = jsonschemavalidator.NewActorFactory(x.vm)
	if e != nil {
		return e
	}
	x.parse, e = get("workflow-parse.js", `json=>JSON.parse(json)`)
	if e != nil {
		return e
	}
	x.stringConstructor, _ = goja.AssertFunction(x.vm.Get("String"))
	x.errorConstructor, _ = goja.AssertFunction(x.vm.Get("Error"))
	x.arrayCheck, _ = goja.AssertFunction(x.vm.Get("Array").ToObject(x.vm).Get("isArray"))
	x.cloneArray, e = get("workflow-array-copy.js", `value=>[...value]`)
	if e != nil {
		return e
	}
	x.allSettled, _ = goja.AssertFunction(x.vm.Get("Promise").ToObject(x.vm).Get("allSettled"))
	x.stringify, e = get("workflow-export.js", `value=>JSON.stringify(value,(_key,v)=>typeof v==='function'?undefined:v)`)
	if e != nil {
		return e
	}
	x.argsStringify, e = get("workflow-args-json.js", `(()=>{const stringify=JSON.stringify;return value=>stringify(value)})()`)
	if e != nil {
		return e
	}
	x.safeStringify, e = get("workflow-safe-json.js", `(()=>{const Weak=WeakSet,stringify=JSON.stringify;return value=>{const seen=new Weak();return stringify(value,(_k,v)=>{if(typeof v==='bigint')return v.toString();if(typeof v==='function')return undefined;if(v!==null&&typeof v==='object'){if(seen.has(v))return '[Circular]';seen.add(v)}return v})??'null'}})()`)
	if e != nil {
		return e
	}
	x.coerce, e = get("workflow-coerce.js", `value=>{if(typeof value==='string')return value;if(value===null)return 'null';if(value===undefined)return 'undefined';if(typeof value==='function')return '[function]';if(typeof value==='object'){try{return JSON.stringify(value)??'[object]'}catch{return '[object]'}}return String(value)}`)
	if e != nil {
		return e
	}
	x.describe, e = get("workflow-error.js", `value=>{if(typeof value==='string')return value;if(value===null||value===undefined||typeof value!=='object')return String(value);const read=k=>{try{const v=value[k];return typeof v==='string'&&v!==''?v:undefined}catch{return undefined}};const m=read('message'),n=read('name');if(m)return n&&n!=='Error'?n+': '+m:m;if(n)return n;try{return JSON.stringify(value)??'[object]'}catch{return '[unprintable thrown value]'}}`)
	if e != nil {
		return e
	}
	_, e = x.vm.RunScript("workflow-determinism.js", `(()=>{const RealDate=Date;globalThis.Date=new Proxy(RealDate,{construct(target,args,newTarget){if(args.length===0)throw new Error('Date.now() / new Date() are unavailable in workflow scripts (breaks resume). Stamp results after the workflow returns, or pass timestamps via args.');return Reflect.construct(target,args,newTarget)},get(target,prop,receiver){if(prop==='now')throw new Error('Date.now() / new Date() are unavailable in workflow scripts (breaks resume). Stamp results after the workflow returns, or pass timestamps via args.');return Reflect.get(target,prop,receiver)}});Math.random=()=>{throw new Error('Math.random() is unavailable in workflow scripts (breaks resume). For N independent samples, include the index in the agent label or prompt.')};const blocked=function(){throw new EvalError('Code generation from strings disallowed for this context')};for(const fn of [function(){},async function(){},function*(){}])Object.defineProperty(Object.getPrototypeOf(fn),'constructor',{value:blocked,writable:false,configurable:false});Object.defineProperty(globalThis,'Function',{value:blocked,writable:false,configurable:false});Object.defineProperty(globalThis,'eval',{value:blocked,writable:false,configurable:false})})()`)
	if e != nil {
		return e
	}
	if e = x.vm.Set("agent", x.nativeAgent); e != nil {
		return e
	}
	if e = x.vm.Set("log", func(call goja.FunctionCall) goja.Value {
		message, e := x.coerce(goja.Undefined(), call.Argument(0))
		if e != nil {
			panic(e)
		}
		if e := x.emit(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("workflow_log")}, protocol.JSONField{Name: "message", Value: quoted(message.String())})); e != nil {
			panic(x.errorValue(e))
		}
		return goja.Undefined()
	}); e != nil {
		return e
	}
	if e = x.vm.Set("phase", func(call goja.FunctionCall) goja.Value {
		title, e := x.coerce(goja.Undefined(), call.Argument(0))
		if e != nil {
			panic(e)
		}
		if title.String() != "" {
			x.phase = title.String()
			if _, e := x.resolvePhase(x.phase, "script"); e != nil {
				panic(x.errorValue(e))
			}
		}
		return goja.Undefined()
	}); e != nil {
		return e
	}
	if e = x.initializeConsole(); e != nil {
		return e
	}
	// These globals are not advertised until their actual producers are bound.
	// Keeping a typed capability here prevents successful-looking partial runs.
	if e := x.vm.Set("parallel", x.parallel); e != nil {
		return e
	}
	if e := x.vm.Set("pipeline", x.pipeline); e != nil {
		return e
	}
	if e := x.vm.Set("setTimeout", x.setTimer); e != nil {
		return e
	}
	if e := x.vm.Set("clearTimeout", x.clearTimer); e != nil {
		return e
	}
	if e := x.vm.Set("workflow", x.nativeNested); e != nil {
		return e
	}
	if e := x.vm.Set("__workflow_spent", func(goja.FunctionCall) goja.Value { return x.vm.ToValue(x.options.GetTurnSpent()) }); e != nil {
		return e
	}
	var total goja.Value = goja.Null()
	if x.options.BudgetTotal != nil {
		total = x.vm.ToValue(*x.options.BudgetTotal)
	}
	if e := x.vm.Set("__workflow_total", total); e != nil {
		return e
	}
	budget, e := x.vm.RunScript("workflow-budget.js", `(()=>{const spent=__workflow_spent,total=__workflow_total;delete globalThis.__workflow_spent;delete globalThis.__workflow_total;return Object.freeze({__proto__:null,total,spent:()=>spent(),remaining:()=>total===null?Infinity:Math.max(0,total-spent())})})()`)
	if e != nil {
		return e
	}
	if e = x.vm.Set("budget", budget); e != nil {
		return e
	}
	args := goja.Undefined()
	if x.options.Args != nil {
		args, e = x.parse(goja.Undefined(), x.vm.ToValue(string(x.options.Args)))
		if e != nil {
			return e
		}
	}
	if e = x.vm.Set("args", args); e != nil {
		return e
	}
	for _, title := range x.options.SeedPhaseTitles {
		if title != "" {
			if _, e = x.resolvePhase(title, "meta"); e != nil {
				return e
			}
		}
	}
	return x.schemaFactory.SealOnActor()
}
func (x *execution) runProgram() (goja.Value, error) {
	timer := time.AfterFunc(30*time.Second, func() { x.vm.Interrupt("Script execution timed out after 30000ms") })
	defer timer.Stop()
	defer x.vm.ClearInterrupt()
	return x.vm.RunProgram(x.options.Program)
}
func (x *execution) emit(event protocol.JSONObject) error {
	if event.String("type") == "workflow_log" && len(x.result.Logs) < 1000 {
		x.result.Logs = append(x.result.Logs, event.String("message"))
	}
	return x.options.Emit(event.Clone())
}
func (x *execution) resolvePhase(title, kind string) (int, error) {
	if index, ok := x.phases[title]; ok {
		return index, nil
	}
	index := len(x.phases) + 1
	x.phases[title] = index
	return index, x.emit(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("workflow_phase")}, protocol.JSONField{Name: "index", Value: valueRaw(index)}, protocol.JSONField{Name: "title", Value: quoted(title)}, protocol.JSONField{Name: "kind", Value: quoted(kind)}))
}
func (x *execution) describeValue(value goja.Value) string {
	text, e := x.describe(goja.Undefined(), value)
	if e != nil {
		return "[unprintable thrown value]"
	}
	return text.String()
}
func (x *execution) describeError(err error) string {
	var exception *goja.Exception
	if errors.As(err, &exception) {
		return x.describeValue(exception.Value())
	}
	return err.Error()
}
func (x *execution) export(value goja.Value, result bool) (json.RawMessage, error) {
	if _, ok := goja.AssertFunction(value); ok && result {
		return nil, errors.New("workflow result cannot be a function")
	}
	if goja.IsUndefined(value) {
		return []byte("null"), nil
	}
	raw, e := x.stringify(goja.Undefined(), value)
	if e != nil {
		return nil, e
	}
	if goja.IsUndefined(raw) {
		return []byte("null"), nil
	}
	return protocol.ParseAndStringifyJavaScriptJSON([]byte(raw.String()))
}

func (x *execution) releaseAgent() { x.options.shared.release(x) }
