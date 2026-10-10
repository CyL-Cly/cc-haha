package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/dop251/goja"
)

const agentCapMessage = "Workflow agent() call cap reached (1000). This usually means a loop using budget.remaining() never terminates because no token budget was set — remaining() returns Infinity when budget.total is null. Add a hard iteration cap to the loop, or pass a token budget."

type AgentProgress struct {
	Tokens, ToolCalls float64
	LastToolName      string
}

func budgetNumber(value float64) string {
	if math.IsNaN(value) {
		return "NaN"
	}
	if math.IsInf(value, 1) {
		return "∞"
	}
	if math.IsInf(value, -1) {
		return "-∞"
	}
	text := strconv.FormatFloat(math.Round(value*1000)/1000, 'f', -1, 64)
	parts := strings.SplitN(text, ".", 2)
	whole := parts[0]
	sign := ""
	if strings.HasPrefix(whole, "-") {
		sign = "-"
		whole = whole[1:]
	}
	for i := len(whole) - 3; i > 0; i -= 3 {
		whole = whole[:i] + "," + whole[i:]
	}
	if len(parts) == 2 {
		return sign + whole + "." + parts[1]
	}
	return sign + whole
}
func (x *execution) assertLimits() error {
	if x.options.shared.count() >= 1000 {
		return &scriptError{Name: "WorkflowAgentCapError", Message: agentCapMessage}
	}
	return x.assertBudget()
}
func (x *execution) assertBudget() error {
	total := x.options.BudgetTotal
	if total == nil || *total <= 0 {
		return nil
	}
	spent := x.options.GetTurnSpent()
	if spent < *total {
		return nil
	}
	return &scriptError{Name: "WorkflowBudgetExceededError", Message: "Workflow token budget exceeded (" + budgetNumber(spent) + " / " + budgetNumber(*total) + " output tokens). Stopping further agent() calls. In-flight agents will complete; their results are preserved."}
}
func clipped(value string, limit int) json.RawMessage {
	value = javascriptTrim(value)
	if value == "" {
		return nil
	}
	units := utf16.Encode([]rune(value))
	if len(units) > limit {
		units = append(units[:limit], 0x2026)
	}
	return protocol.QuoteJavaScriptUTF16(units)
}
func agentLabel(prompt string, options protocol.JSONObject) string {
	value := options.String("label")
	if options.Get("label") == nil {
		units := utf16.Encode([]rune(prompt))
		if len(units) > 60 {
			units = units[:60]
		}
		value = string(utf16.Decode(units))
	}
	var out strings.Builder
	space := false
	for _, r := range value {
		if javascriptSpace(r) {
			if !space {
				out.WriteByte(' ')
			}
			space = true
		} else {
			out.WriteRune(r)
			space = false
		}
	}
	value = javascriptTrim(out.String())
	if value == "" {
		return "agent"
	}
	return value
}
func (x *execution) post(ctx context.Context, run func() error) error {
	ack := make(chan error, 1)
	// This is delivery to the original async producer, not a workflow script
	// rejection. The producer must retain its own catch/wrapper/promise stage.
	reaction := runtimeReaction{run: func() error { e := run(); ack <- e; return nil }}
	select {
	case x.reactions <- reaction:
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-x.ctx.Done():
		return context.Cause(x.ctx)
	}
	select {
	case e := <-ack:
		return e
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-x.ctx.Done():
		return context.Cause(x.ctx)
	}
}
func (x *execution) appendJournal(entry protocol.JSONObject) {
	if x.options.Journal != nil {
		_, _ = x.options.Journal.BeginAppend(entry)
	}
}
func (x *execution) flushJournal() error {
	if x.options.Journal == nil {
		return nil
	}
	done, result := x.options.Journal.PendingFlush()
	<-done
	return result()
}

func (x *execution) nativeAgent(call goja.FunctionCall) goja.Value {
	promise, resolve, reject := x.vm.NewPromise()
	if x.ctx.Err() != nil {
		return x.vm.ToValue(promise)
	}
	prompt, e := x.coerce(goja.Undefined(), call.Argument(0))
	if e != nil {
		_ = reject(x.errorValue(e))
		return x.vm.ToValue(promise)
	}
	opts, e := x.captureOptions(call.Argument(1))
	if e != nil {
		_ = reject(x.errorValue(e))
		return x.vm.ToValue(promise)
	}
	schema, e := x.captureSchema(call.Argument(1))
	if e != nil {
		_ = reject(x.errorValue(e))
		return x.vm.ToValue(promise)
	}
	if e := x.assertLimits(); e != nil {
		_ = reject(x.errorValue(e))
		return x.vm.ToValue(promise)
	}
	index, admitted := x.options.shared.next()
	if !admitted {
		_ = reject(x.errorValue(&scriptError{Name: "WorkflowAgentCapError", Message: agentCapMessage}))
		return x.vm.ToValue(promise)
	}
	phaseTitle := x.phase
	hasPhase := x.phase != ""
	if opts.Get("phase") != nil {
		phaseTitle = opts.String("phase")
		hasPhase = true
	}
	phase := 0
	if hasPhase {
		phase, e = x.resolvePhase(phaseTitle, "script")
		if e != nil {
			_ = reject(x.errorValue(e))
			return x.vm.ToValue(promise)
		}
	}
	label := agentLabel(prompt.String(), opts)
	model := x.options.Model
	if opts.Get("model") != nil {
		model = opts.String("model")
	}
	preview := clipped(prompt.String(), 400)
	if opts.String("isolation") == "remote" {
		_ = reject(x.errorValue(errors.New("agent({isolation:'remote'}) is not available in this build")))
		return x.vm.ToValue(promise)
	}
	isolationUnavailable := ""
	if opts.String("isolation") == "worktree" {
		if x.options.WorktreeUnavailableReason == nil {
			_ = reject(x.errorValue(&CapabilityError{"actual worktree availability producer"}))
			return x.vm.ToValue(promise)
		}
		isolationUnavailable, e = x.options.WorktreeUnavailableReason()
		if e != nil {
			_ = reject(x.errorValue(e))
			return x.vm.ToValue(promise)
		}
		if isolationUnavailable != "" && !x.isolationReported {
			x.isolationReported = true
			if e := x.emit(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("workflow_log")}, protocol.JSONField{Name: "message", Value: quoted("worktree isolation unavailable (" + isolationUnavailable + ") — agents run in the workspace directory. Run `git init` there, or configure WorktreeCreate/WorktreeRemove hooks, for isolated runs.")})); e != nil {
				_ = reject(x.errorValue(e))
				return x.vm.ToValue(promise)
			}
		}
	}
	if schema != nil && x.options.Journal != nil {
		raw, e := x.schemaCacheJSON(schema)
		if e != nil {
			_ = reject(x.errorValue(e))
			return x.vm.ToValue(promise)
		}
		opts.Set("schema", raw)
	}
	var key json.RawMessage
	if x.options.Journal != nil {
		key, e = CacheKey(quoted(prompt.String()), valueRaw(opts), x.previousCacheKey)
		if e != nil {
			_ = reject(x.errorValue(e))
			return x.vm.ToValue(promise)
		}
		x.previousCacheKey = key
		if !x.cacheExhausted && x.options.JournalSnapshot != nil {
			if cached, ok := x.options.JournalSnapshot.Results[journalKey(key)]; ok {
				now := x.options.Now()
				event := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("workflow_agent")}, protocol.JSONField{Name: "index", Value: valueRaw(index)}, protocol.JSONField{Name: "label", Value: quoted(label)}, protocol.JSONField{Name: "state", Value: quoted("done")})
				if hasPhase {
					event.Set("phaseIndex", valueRaw(phase))
					event.Set("phaseTitle", quoted(phaseTitle))
				}
				event.Set("model", quoted(model))
				event.Set("agentId", cached.AgentID)
				event.Set("startedAt", valueRaw(now))
				event.Set("lastProgressAt", valueRaw(now))
				event.Set("cached", []byte("true"))
				event.Set("promptPreview", preview)
				value, e := x.parse(goja.Undefined(), x.vm.ToValue(string(defaultRaw(cached.Result, []byte("null")))))
				if e != nil {
					_ = reject(x.errorValue(e))
					return x.vm.ToValue(promise)
				}
				text, e := x.coerce(goja.Undefined(), value)
				if e != nil {
					_ = reject(x.errorValue(e))
					return x.vm.ToValue(promise)
				}
				event.Set("resultPreview", clipped(text.String(), 400))
				if e := x.emit(event); e != nil {
					_ = reject(x.errorValue(e))
					return x.vm.ToValue(promise)
				}
				_ = resolve(value)
				return x.vm.ToValue(promise)
			}
		}
		x.cacheExhausted = true
	}
	queued := x.options.Now()
	base := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("workflow_agent")}, protocol.JSONField{Name: "index", Value: valueRaw(index)}, protocol.JSONField{Name: "label", Value: quoted(label)}, protocol.JSONField{Name: "state", Value: quoted("start")})
	if hasPhase {
		base.Set("phaseIndex", valueRaw(phase))
		base.Set("phaseTitle", quoted(phaseTitle))
	}
	base.Set("model", quoted(model))
	base.Set("queuedAt", valueRaw(queued))
	base.Set("lastProgressAt", valueRaw(queued))
	base.Set("promptPreview", preview)
	if opts.String("agentType") != "" {
		base.Set("agentType", opts.Get("agentType"))
	}
	if opts.String("isolation") == "worktree" && isolationUnavailable == "" {
		base.Set("isolation", quoted("worktree"))
	}
	if e := x.emit(base); e != nil {
		_ = reject(x.errorValue(e))
		return x.vm.ToValue(promise)
	}
	params := AgentExecution{Prompt: prompt.String(), Options: opts, Index: index, PhaseIndex: phase, PhaseTitle: phaseTitle, WorkflowName: x.options.WorkflowName, RunID: x.options.RunID, Schema: schema}
	start := func() {
		if x.ctx.Err() != nil {
			x.releaseAgent()
			return
		}
		if e := x.assertBudget(); e != nil {
			x.releaseAgent()
			_ = reject(x.errorValue(e))
			return
		}
		agentCtx, agentCancel := context.WithCancelCause(x.ctx)
		agentKey := fmt.Sprintf("%s-%d", x.options.RunID, index)
		if x.options.OnAgentController != nil {
			if e := x.options.OnAgentController(agentKey, agentCancel); e != nil {
				x.releaseAgent()
				_ = reject(x.errorValue(e))
				agentCancel(e)
				return
			}
		}
		x.controllers[agentKey] = agentCancel
		started := x.options.Now()
		lastProgress := started
		tokens, toolCalls := 0.0, 0.0
		agentID := ""
		progress := func(extra protocol.JSONObject) error {
			event := base.Clone()
			event.Set("state", quoted("progress"))
			event.Set("startedAt", valueRaw(started))
			event.Set("lastProgressAt", valueRaw(lastProgress))
			event.Assign(extra)
			return x.emit(event)
		}
		if e := progress(protocol.NewJSONObject()); e != nil {
			delete(x.controllers, agentKey)
			if x.options.OnAgentController != nil {
				_ = x.options.OnAgentController(agentKey, nil)
			}
			x.releaseAgent()
			_ = reject(x.errorValue(e))
			agentCancel(e)
			return
		}
		params.OnAgentID = func(id string) error {
			return x.post(agentCtx, func() error {
				agentID = id
				cacheKey := key
				if cacheKey == nil {
					cacheKey = quoted("")
				}
				x.appendJournal(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("started")}, protocol.JSONField{Name: "key", Value: cacheKey}, protocol.JSONField{Name: "agentId", Value: quoted(id)}))
				return nil
			})
		}
		params.OnProgress = func(value AgentProgress) error {
			return x.post(agentCtx, func() error {
				tokens, toolCalls = value.Tokens, value.ToolCalls
				lastProgress = x.options.Now()
				extra := protocol.NewJSONObject(protocol.JSONField{Name: "tokens", Value: valueRaw(tokens)}, protocol.JSONField{Name: "toolCalls", Value: valueRaw(toolCalls)})
				if agentID != "" {
					extra.Set("agentId", quoted(agentID))
				}
				if value.LastToolName != "" {
					extra.Set("lastToolName", quoted(value.LastToolName))
				}
				return progress(extra)
			})
		}
		stall := 180000.0
		if opts.Get("stallMs") != nil {
			stall = jsonNumber(opts.Get("stallMs"))
		}
		interval := math.Max(5000, math.Floor(stall/2))
		// Native Node timers clamp overflowing intervals to one millisecond.
		if interval > math.MaxInt32 {
			interval = 1
		}
		x.jobs.Add(1)
		go func() {
			defer x.jobs.Done()
			ticker := time.NewTicker(time.Duration(interval) * time.Millisecond)
			defer ticker.Stop()
			finished := make(chan struct{})
			var outcome AgentOutcome
			var cause error
			go func() { outcome, cause = x.options.RunAgent(agentCtx, params); close(finished) }()
			for {
				select {
				case <-finished:
					goto settled
				case <-ticker.C:
					_ = x.post(agentCtx, func() error {
						if x.options.Now()-lastProgress < stall {
							return nil
						}
						extra := protocol.NewJSONObject(protocol.JSONField{Name: "tokens", Value: valueRaw(tokens)}, protocol.JSONField{Name: "toolCalls", Value: valueRaw(toolCalls)})
						if agentID != "" {
							extra.Set("agentId", quoted(agentID))
						}
						return progress(extra)
					})
				case <-agentCtx.Done():
					<-finished
					goto settled
				}
			}
		settled:
			reaction := runtimeReaction{run: func() error {
				cleaned := false
				finish := func() {
					if cleaned {
						return
					}
					cleaned = true
					agentCancel(nil)
					delete(x.controllers, agentKey)
					if x.options.OnAgentController != nil {
						_ = x.options.OnAgentController(agentKey, nil)
					}
					x.releaseAgent()
				}
				defer finish()
				now := x.options.Now()
				event := base.Clone()
				event.Set("agentId", nil)
				if agentID != "" {
					event.Set("agentId", quoted(agentID))
				}
				event.Set("startedAt", valueRaw(started))
				event.Set("lastProgressAt", valueRaw(now))
				event.Set("durationMs", valueRaw(now-started))
				if cause != nil {
					event.Set("state", quoted("error"))
					event.Set("tokens", valueRaw(tokens))
					event.Set("toolCalls", valueRaw(toolCalls))
					skipped := agentCtx.Err() != nil && strings.Contains(fmt.Sprint(context.Cause(agentCtx)), "user-skip")
					message := cause.Error()
					if skipped {
						message = "skipped by user"
						event.Set("skipped", []byte("true"))
					}
					event.Set("error", quoted(message))
					if e := x.emit(event); e != nil {
						return e
					}
					if x.ctx.Err() != nil {
						return nil
					}
					finish()
					if skipped {
						return resolve(goja.Null())
					}
					return reject(x.errorValue(cause))
				}
				event.Set("state", quoted("done"))
				event.Set("agentId", quoted(outcome.AgentID))
				event.Set("tokens", valueRaw(outcome.Tokens))
				event.Set("toolCalls", valueRaw(outcome.ToolCalls))
				value, e := x.parse(goja.Undefined(), x.vm.ToValue(string(defaultRaw(outcome.Value, []byte("null")))))
				if e != nil {
					return reject(x.errorValue(e))
				}
				text, e := x.coerce(goja.Undefined(), value)
				if e != nil {
					return reject(x.errorValue(e))
				}
				event.Set("resultPreview", clipped(text.String(), 400))
				if e := x.emit(event); e != nil {
					return e
				}
				if key != nil {
					x.appendJournal(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("result")}, protocol.JSONField{Name: "key", Value: key}, protocol.JSONField{Name: "agentId", Value: quoted(outcome.AgentID)}, protocol.JSONField{Name: "result", Value: outcome.Value}))
				}
				finish()
				return resolve(value)
			}}
			select {
			case x.reactions <- reaction:
			case <-x.ctx.Done():
				agentCancel(context.Cause(x.ctx))
			}
		}()
	}
	if x.options.shared.admit(runtimeAdmission{actor: x, ctx: x.ctx, start: start, wake: x.wakeAdmission}) {
		start()
	}
	return x.vm.ToValue(promise)
}
