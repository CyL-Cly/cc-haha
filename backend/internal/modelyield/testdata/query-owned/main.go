package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelyield"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Profile struct {
	ID                string `json:"id"`
	ReturnValue       string `json:"returnValue"`
	CallerAbortReason string `json:"callerAbortReason"`
	Production        struct {
		HistorySources                         []string                `json:"historySources"`
		SystemSources                          []json.RawMessage       `json:"systemSources"`
		Thinking                               requestcontext.Thinking `json:"thinking"`
		Tools                                  []protocol.JSONObject   `json:"tools"`
		SDKBetas                               []string                `json:"sdkBetas"`
		SessionID, DeviceID, PermissionProfile string
		Options                                requestcontext.Options `json:"options"`
	} `json:"production"`
}
type Input struct {
	Profile      Profile `json:"profile"`
	Home, Origin string
}

func uuid() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func raw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(2)
	}
}
func run() error {
	reader := bufio.NewReader(os.Stdin)
	first, e := reader.ReadBytes('\n')
	if e != nil {
		return e
	}
	var in Input
	if e = json.Unmarshal(first, &in); e != nil {
		return e
	}
	home, e := filepath.EvalSymlinks(in.Home)
	if e != nil || home != os.Getenv("HOME") || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(home, ".claude") || os.Getenv("CC_HAHA_QUERY_CONSUMER_STOP") != "1" || os.Getenv("ANTHROPIC_BASE_URL") != in.Origin || os.Getenv("ANTHROPIC_API_KEY") != "offline-fixture-key" || !strings.HasPrefix(filepath.Base(home), "cc-haha-actual-consumer-") {
		return errors.New("explicit private HOME/credential/origin required")
	}
	if !strings.HasPrefix(in.Origin, "http://127.0.0.1:") {
		return errors.New("private original loopback required")
	}
	p := in.Profile
	known := map[string]bool{"normal-tail-control": true, "return-at-yield": true, "return-queued-after-next": true, "caller-abort-after-next": true, "cleanup-resource-after-next": true}
	if !known[p.ID] || p.ReturnValue != "original-consumer-return" || p.CallerAbortReason != "interrupt" || p.Production.PermissionProfile != "actual getEmptyToolPermissionContext/default" || len(p.Production.Tools) != 0 || len(p.Production.SystemSources) != 0 || p.Production.Thinking.Type != "disabled" || p.Production.Options.QuerySource != "sdk" || !p.Production.Options.IsNonInteractiveSession {
		return errors.New("original finite production input required")
	}
	globalBytes, e := os.ReadFile(filepath.Join(home, ".claude/.claude.json"))
	if e != nil {
		return e
	}
	var global protocol.JSONObject
	if e = global.UnmarshalJSON(globalBytes); e != nil || global.String("userID") != p.Production.DeviceID {
		return errors.New("original global device identity mismatch")
	}
	env := map[string]string{}
	for _, key := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY", "CLAUDE_CODE_SIMPLE", "CLAUDE_CODE_MAX_RETRIES"} {
		value, ok := os.LookupEnv(key)
		if ok {
			env[key] = value
		}
	}
	// These original process fields belong to disabled attempt policies. Their
	// exact original values are admitted, rather than deleted to bypass a guard.
	for key, value := range map[string]string{"CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK": "1", "CLAUDE_STREAM_TRANSIENT_RETRY_MAX": "0", "CLAUDE_ENABLE_STREAM_WATCHDOG": "0", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY": "1"} {
		if os.Getenv(key) != value {
			return fmt.Errorf("unported active original policy %s", key)
		}
	}
	request := requestretry.Input{Context: requestcontext.Context{SystemPrompt: p.Production.SystemSources, Tools: p.Production.Tools, Thinking: p.Production.Thinking, Options: p.Production.Options, Host: requestcontext.Host{Profile: requestcontext.DirectRelayProfile, Env: env, DeviceID: global.String("userID"), SessionID: p.Production.SessionID, SDKBetas: p.Production.SDKBetas}}, Runtime: provider.SourceRuntime{Platform: "darwin", Arch: "arm64", Version: "v24.3.0"}}
	for _, source := range p.Production.HistorySources {
		var item protocol.JSONObject
		if e = item.UnmarshalJSON([]byte(source)); e != nil {
			return e
		}
		request.Context.History = append(request.Context.History, item)
	}
	// Original options.fastMode is absent. queryModel's !!options.fastMode is
	// false in this finite direct query profile, independently of Source output.
	fast := false
	request.ResolvedFastMode = &fast
	owner := cooperative.New()
	defer owner.Close()
	mainBinding, e := modelaccounting.NewMainContext(owner)
	if e != nil {
		return e
	}
	session, e := modelaccounting.NewOwnedSession(owner, nil)
	if e != nil {
		return e
	}
	caller, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	var timeline []map[string]any
	var values []modelyield.QueryValue
	var settlements []map[string]any
	var at []string
	nextCalls, returnCalls := 0, 0
	var control *modelyield.ResourceControl
	snapshot := func(scope *cooperative.Scope) map[string]any {
		account, _ := session.Snapshot(scope)
		sdk := any(nil)
		if control != nil {
			present, aborted, _ := control.State(scope)
			if present {
				reason := map[string]any{"defined": false}
				if aborted {
					reason = map[string]any{"defined": true, "value": map[string]any{"class": "DOMException", "name": "AbortError", "message": "The operation was aborted.", "implementation": "Go owned SDK resource context; no Source stack"}}
				}
				sdk = map[string]any{"aborted": aborted, "reason": reason}
			}
		}
		reason := map[string]any{"defined": false}
		if caller.Err() != nil {
			reason = map[string]any{"defined": true, "value": map[string]any{"primitive": context.Cause(caller).Error()}}
		}
		return map[string]any{"caller": map[string]any{"aborted": caller.Err() != nil, "reason": reason}, "sdk": sdk, "accounting": account}
	}
	mark := func(scope *cooperative.Scope, kind string, data map[string]any) {
		if data == nil {
			data = map[string]any{}
		}
		data["kind"] = kind
		data["states"] = snapshot(scope)
		timeline = append(timeline, data)
	}
	valueJSON := func(value modelyield.QueryValue) json.RawMessage {
		if value.Yield == nil {
			return value.ReturnValue
		}
		if value.Yield.Update != nil {
			u := value.Yield.Update
			if u.Envelope != nil {
				b, _ := u.Envelope.MarshalJSON()
				return b
			}
			b, _ := u.EventJSON()
			return b
		}
		return raw(value.Yield.Notice)
	}
	resultJSON := func(r modelyield.QueryResult) json.RawMessage {
		v := protocol.NewJSONObject()
		if r.Defined {
			v.Set("value", valueJSON(r.Value))
		}
		v.Set("done", raw(r.Done))
		b, _ := v.MarshalJSON()
		return b
	}
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		var iterator *modelyield.QueryIterator
		var finished *ownerasync.OwnerFuture[struct{}]
		sendControl := func(next *cooperative.Scope, action string, continueWith func(*cooperative.Scope) error) error {
			mark(next, "consumer-parent-control", map[string]any{"action": action})
			fmt.Println(string(raw(map[string]any{"kind": "control", "action": action})))
			future, _ := ownerasync.NewOwnerFuture[json.RawMessage](owner)
			// Only the physical stdin adapter reads immutable bytes off-owner. It never
			// receives a Scope, getter, parser, model policy or Source phase observation.
			go func() {
				bytes, e := reader.ReadBytes('\n')
				copy := append(json.RawMessage(nil), bytes...)
				_, submit := owner.SubmitIndependent(context.Background(), cooperative.Read, func(fresh *cooperative.Scope) error {
					if e != nil {
						return future.Reject(fresh, e)
					}
					return future.Resolve(fresh, copy)
				})
				if submit != nil {
					_ = future.RejectAdministrative(submit)
				}
			}()
			_, e := future.Then(next, func(fresh *cooperative.Scope, bytes json.RawMessage, rejected error) error {
				if rejected != nil {
					return finished.Reject(fresh, rejected)
				}
				var receipt map[string]any
				if e := json.Unmarshal(bytes, &receipt); e != nil {
					return finished.Reject(fresh, e)
				}
				if receipt["action"] != action || receipt["kind"] != "parent-receipt" {
					return finished.Reject(fresh, errors.New("original parent receipt mismatch"))
				}
				mark(fresh, "parent-control-receipt", map[string]any{"receipt": receipt})
				if e := continueWith(fresh); e != nil {
					return finished.Reject(fresh, e)
				}
				return nil
			})
			return e
		}
		fulfill := func(next *cooperative.Scope, label string, r modelyield.QueryResult, rejected error) {
			if rejected != nil {
				settlements = append(settlements, map[string]any{"label": label, "status": "rejected", "error": map[string]any{"message": rejected.Error()}})
				mark(next, "consumer-settled", map[string]any{"label": label, "status": "rejected", "error": rejected.Error()})
				return
			}
			settlements = append(settlements, map[string]any{"label": label, "status": "fulfilled", "value": r})
			if !r.Done {
				values = append(values, r.Value)
				at = append(at, string(valueJSON(r.Value)))
			}
			mark(next, "consumer-settled", map[string]any{"label": label, "status": "fulfilled", "originalSourceAtSettlement": string(resultJSON(r)), "value": r})
		}
		complete := func(next *cooperative.Scope) error {
			mark(next, "consumer-program-completed", nil)
			action := "observe-peer-close"
			if p.ID == "normal-tail-control" {
				action = "observe-socket-now"
			}
			return sendControl(next, action, func(fresh *cooperative.Scope) error { return finished.Resolve(fresh, struct{}{}) })
		}
		var consume func(*cooperative.Scope, string) error
		consume = func(next *cooperative.Scope, prefix string) error {
			nextCalls++
			label := fmt.Sprintf("%s%d", prefix, nextCalls)
			mark(next, "next-call", map[string]any{"label": label})
			future, e := iterator.Next(next)
			if e != nil {
				return e
			}
			_, e = future.Then(next, func(fresh *cooperative.Scope, r modelyield.QueryResult, rejected error) error {
				fulfill(fresh, label, r, rejected)
				if rejected != nil || r.Done {
					return complete(fresh)
				}
				if e := consume(fresh, prefix); e != nil {
					return finished.Reject(fresh, e)
				}
				return nil
			})
			return e
		}
		start := func(next *cooperative.Scope, it *modelyield.QueryIterator, c *modelyield.ResourceControl) (*ownerasync.OwnerFuture[struct{}], error) {
			iterator, control = it, c
			finished, _ = ownerasync.NewOwnerFuture[struct{}](owner)
			mark(next, "original-request-entry", nil)
			nextCalls++
			mark(next, "next-call", map[string]any{"label": "first-next"})
			first, e := iterator.Next(next)
			if e != nil {
				return nil, e
			}
			_, e = first.Then(next, func(fresh *cooperative.Scope, r modelyield.QueryResult, rejected error) error {
				fulfill(fresh, "first-next", r, rejected)
				if rejected != nil {
					return finished.Reject(fresh, rejected)
				}
				if p.ID == "normal-tail-control" {
					return sendControl(fresh, "release-original-tail", func(next *cooperative.Scope) error { return consume(next, "next-") })
				}
				if p.ID == "return-at-yield" {
					returnCalls++
					mark(fresh, "return-call", map[string]any{"pendingNext": false, "originalValue": p.ReturnValue})
					returned, e := iterator.Return(fresh, modelyield.QueryValue{ReturnValue: raw(p.ReturnValue)})
					if e != nil {
						return finished.Reject(fresh, e)
					}
					_, e = returned.Then(fresh, func(next *cooperative.Scope, r modelyield.QueryResult, rejected error) error {
						fulfill(next, "return", r, rejected)
						return complete(next)
					})
					return e
				}
				nextCalls++
				mark(fresh, "next-call", map[string]any{"label": "pending-next"})
				pending, e := iterator.Next(fresh)
				if e != nil {
					return finished.Reject(fresh, e)
				}
				awaitPending := func(next *cooperative.Scope, after func(*cooperative.Scope, modelyield.QueryResult, error) error) error {
					_, e := pending.Then(next, func(fresh *cooperative.Scope, r modelyield.QueryResult, rejected error) error {
						fulfill(fresh, "pending-next", r, rejected)
						return after(fresh, r, rejected)
					})
					return e
				}
				if p.ID == "return-queued-after-next" {
					returnCalls++
					mark(fresh, "return-call", map[string]any{"pendingNext": true, "originalValue": p.ReturnValue})
					returned, e := iterator.Return(fresh, modelyield.QueryValue{ReturnValue: raw(p.ReturnValue)})
					if e != nil {
						return finished.Reject(fresh, e)
					}
					return sendControl(fresh, "release-original-tail", func(next *cooperative.Scope) error {
						return awaitPending(next, func(fresh *cooperative.Scope, _ modelyield.QueryResult, _ error) error {
							_, e := returned.Then(fresh, func(next *cooperative.Scope, r modelyield.QueryResult, rejected error) error {
								fulfill(next, "return", r, rejected)
								return complete(next)
							})
							return e
						})
					})
				}
				if p.ID == "caller-abort-after-next" {
					cancel(errors.New(p.CallerAbortReason))
					mark(fresh, "original-caller-abort", nil)
				} else {
					if e := control.Cleanup(fresh); e != nil {
						return finished.Reject(fresh, e)
					}
					mark(fresh, "actual-cleanupStream-return", nil)
				}
				return awaitPending(fresh, func(next *cooperative.Scope, r modelyield.QueryResult, rejected error) error {
					if rejected == nil && !r.Done {
						return consume(next, "production-continuation-next-")
					}
					return complete(next)
				})
			})
			return finished, e
		}
		_, e := modelyield.RunQueued(caller, request, modelyield.QueuedDependencies{AgentContext: mainBinding, Request: requestretry.Dependencies{StreamOwner: owner, StreamScope: scope, UUID: uuid, Now: time.Now, Random: func() float64 { return .5 }, Sleep: func(context.Context, float64) error { return errors.New("unported active retry sleep") }}, Session: session, Start: start})
		if e != nil {
			return e
		}
		encodedValues := []json.RawMessage{}
		after := []string{}
		for _, v := range values {
			b := valueJSON(v)
			encodedValues = append(encodedValues, b)
			after = append(after, string(b))
		}
		encodedSettlements := []map[string]any{}
		for _, s := range settlements {
			copy := map[string]any{}
			for k, v := range s {
				copy[k] = v
			}
			if r, ok := s["value"].(modelyield.QueryResult); ok {
				copy["value"] = resultJSON(r)
			}
			encodedSettlements = append(encodedSettlements, copy)
		}
		observation := map[string]any{"id": p.ID, "nextCalls": nextCalls, "returnCalls": returnCalls, "values": encodedValues, "settlements": encodedSettlements, "states": snapshot(scope), "timeline": timeline, "yieldSourcesAtSettlement": at, "yieldSourcesAfterProgram": after, "historyBefore": p.Production.HistorySources, "historyAfter": p.Production.HistorySources, "producer": "actual RunQueued/same Run producer/requestretry/Open/OwnedMessageStream/SDKstream/StreamHistory"}
		fmt.Println(string(raw(map[string]any{"kind": "result", "pid": os.Getpid(), "home": home, "observation": observation, "termination": "explicit child exit after original program/socket receipt; no host Record join"})))
		return nil
	})
	return err
}
