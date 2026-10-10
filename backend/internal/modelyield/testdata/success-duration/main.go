package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/apipresentation"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelyield"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
)

type Specification struct {
	Production struct {
		HistorySources                         []string
		SystemSources                          []json.RawMessage
		Thinking                               requestcontext.Thinking
		Tools                                  []protocol.JSONObject
		SDKBetas                               []string
		SessionID, DeviceID, PermissionProfile string
		Options                                requestcontext.Options
	}
	Program, ReturnValue, CallerAbortReason, ThrowMessage, ResponseRequestID string
	Permission                                                               struct{ Initial, AtMessageDelta string }
	ClockAtStart, ClockAtMessageDelta, ClockAtPermissionRelease              float64
}
type Original struct {
	ID                       string
	Clock                    struct{ UnixMillis float64 }
	InitialLastMainRequestID *string
	Requests                 []Specification
}
type Input struct {
	Original     Original
	Home, Origin string
}

func raw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func uuid() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(2)
	}
}
func run() error {
	reader := bufio.NewReader(os.Stdin)
	line, e := reader.ReadBytes('\n')
	if e != nil {
		return e
	}
	var in Input
	if e = json.Unmarshal(line, &in); e != nil {
		return e
	}
	home, e := filepath.EvalSymlinks(in.Home)
	if e != nil || home != os.Getenv("HOME") || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(home, ".claude") || os.Getenv("CC_HAHA_DURATION_SOURCE") != "1" || os.Getenv("TZ") != "UTC" || os.Getenv("NODE_ENV") != "production" || os.Getenv("ANTHROPIC_BASE_URL") != in.Origin || os.Getenv("ANTHROPIC_API_KEY") != "offline-fixture-key" || !strings.HasPrefix(in.Origin, "http://127.0.0.1:") {
		return errors.New("explicit private original duration input required")
	}
	owner := cooperative.New()
	defer owner.Close()
	session, e := modelaccounting.NewOwnedSession(owner, in.Original.InitialLastMainRequestID)
	if e != nil {
		return e
	}
	binding, _ := modelaccounting.NewMainContext(owner)
	clock := in.Original.Clock.UnixMillis
	var requests []map[string]any
	var allValues []json.RawMessage
	var timeline []map[string]any
	e = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		accounting := func(s *cooperative.Scope) protocol.JSONObject {
			v, _ := session.Snapshot(s)
			d, _ := session.DurationSnapshot(s)
			v.Assign(d)
			return v
		}
		for index, spec := range in.Original.Requests {
			ordinal := index + 1
			clock = spec.ClockAtStart
			if spec.Production.PermissionProfile != "actual getEmptyToolPermissionContext/default" || len(spec.Production.Tools) != 0 || spec.Production.Thinking.Type != "disabled" {
				return errors.New("unported original duration input")
			}
			env := map[string]string{}
			for _, k := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY", "CLAUDE_CODE_SIMPLE", "CLAUDE_CODE_MAX_RETRIES"} {
				if v, ok := os.LookupEnv(k); ok {
					env[k] = v
				}
			}
			for k, v := range map[string]string{"CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK": "1", "CLAUDE_STREAM_TRANSIENT_RETRY_MAX": "0", "CLAUDE_ENABLE_STREAM_WATCHDOG": "0", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY": "1"} {
				if os.Getenv(k) != v {
					return fmt.Errorf("unported active original policy %s", k)
				}
			}
			original := requestretry.Input{Context: requestcontext.Context{SystemPrompt: spec.Production.SystemSources, Tools: spec.Production.Tools, Thinking: spec.Production.Thinking, Options: spec.Production.Options, Host: requestcontext.Host{Profile: requestcontext.DirectRelayProfile, Env: env, DeviceID: spec.Production.DeviceID, SessionID: spec.Production.SessionID, SDKBetas: spec.Production.SDKBetas}}, Runtime: provider.SourceRuntime{Platform: "darwin", Arch: "arm64", Version: "v24.3.0"}}
			for _, source := range spec.Production.HistorySources {
				var item protocol.JSONObject
				if e := item.UnmarshalJSON([]byte(source)); e != nil {
					return e
				}
				original.Context.History = append(original.Context.History, item)
			}
			falseFast := false
			original.ResolvedFastMode = &falseFast
			permission := protocol.NewJSONObject(protocol.JSONField{Name: "mode", Value: raw("default")})
			initial, _ := ownerasync.NewOwnerFuture[protocol.JSONObject](owner)
			if e := initial.Resolve(scope, permission); e != nil {
				return e
			}
			held, _ := ownerasync.NewOwnerFuture[protocol.JSONObject](owner)
			holder := initial
			caller, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			var controls *modelyield.ResourceControl
			var iterator *modelyield.QueryIterator
			finished, _ := ownerasync.NewOwnerFuture[struct{}](owner)
			var yielded []json.RawMessage
			var settlements []map[string]any
			var getters []map[string]any
			mark := func(kind string) {
				timeline = append(timeline, map[string]any{"kind": kind, "ordinal": ordinal, "currentUnixMillis": clock, "accounting": accounting(scope)})
			}
			send := func(s *cooperative.Scope, action string, cont func(*cooperative.Scope) error) error {
				fmt.Println(string(raw(map[string]any{"kind": "control", "action": action, "ordinal": ordinal})))
				receipt, _ := ownerasync.NewOwnerFuture[json.RawMessage](owner)
				go func() {
					b, e := reader.ReadBytes('\n')
					copy := append(json.RawMessage(nil), b...)
					_, err := owner.SubmitIndependent(context.Background(), cooperative.Read, func(fresh *cooperative.Scope) error {
						if e != nil {
							return receipt.Reject(fresh, e)
						}
						return receipt.Resolve(fresh, copy)
					})
					if err != nil {
						_ = receipt.RejectAdministrative(err)
					}
				}()
				_, e := receipt.Then(s, func(fresh *cooperative.Scope, b json.RawMessage, rejected error) error {
					if rejected != nil {
						return finished.Reject(fresh, rejected)
					}
					var r struct {
						Action  string
						Ordinal int
						Done    bool
					}
					if e := json.Unmarshal(b, &r); e != nil {
						return finished.Reject(fresh, e)
					}
					if r.Action != action || r.Ordinal != ordinal || !r.Done {
						return finished.Reject(fresh, errors.New("original physical control receipt mismatch"))
					}
					if e := cont(fresh); e != nil {
						return finished.Reject(fresh, e)
					}
					return nil
				})
				return e
			}
			valueJSON := func(v modelyield.QueryValue) json.RawMessage {
				if v.Yield == nil {
					return v.ReturnValue
				}
				if v.Yield.Update == nil {
					return raw(v.Yield.Notice)
				}
				u := v.Yield.Update
				if u.Envelope != nil {
					b, _ := u.Envelope.MarshalJSON()
					return b
				}
				b, _ := u.EventJSON()
				return b
			}
			nextCalls := 0
			delta := false
			var consume func(*cooperative.Scope, *ownerasync.OwnerFuture[modelyield.QueryResult], string) error
			complete := func(s *cooperative.Scope) error { return finished.Resolve(s, struct{}{}) }
			consume = func(s *cooperative.Scope, future *ownerasync.OwnerFuture[modelyield.QueryResult], method string) error {
				_, e := future.Then(s, func(fresh *cooperative.Scope, r modelyield.QueryResult, rejected error) error {
					if rejected != nil {
						settlements = append(settlements, map[string]any{"method": method, "status": "rejected", "message": rejected.Error()})
						mark("consumer-rejected")
						return complete(fresh)
					}
					value := protocol.NewJSONObject()
					if r.Defined {
						value.Set("value", valueJSON(r.Value))
					}
					value.Set("done", raw(r.Done))
					settlements = append(settlements, map[string]any{"method": method, "status": "fulfilled", "result": value})
					mark("consumer-fulfilled")
					if !r.Done {
						source := valueJSON(r.Value)
						yielded = append(yielded, source)
						allValues = append(allValues, source)
					}
					if method == "throw" {
						return send(fresh, "release-original-ending", func(s *cooperative.Scope) error {
							if r.Done {
								return complete(s)
							}
							nextCalls++
							f, e := iterator.Next(s)
							if e != nil {
								return e
							}
							return consume(s, f, "next")
						})
					}
					if r.Done {
						if method == "return" {
							return send(fresh, "release-original-ending", complete)
						}
						return complete(fresh)
					}
					if nextCalls == 1 {
						nextCalls++
						return send(fresh, "release-original-middle", func(s *cooperative.Scope) error {
							f, e := iterator.Next(s)
							if e != nil {
								return e
							}
							return consume(s, f, "next")
						})
					}
					event := protocol.NewJSONObject()
					if r.Value.Yield != nil && r.Value.Yield.Update != nil {
						_ = event.UnmarshalJSON(r.Value.Yield.Update.Event)
					}
					if event.String("type") == "message_delta" && !delta {
						delta = true
						clock = spec.ClockAtMessageDelta
						if spec.Permission.AtMessageDelta == "replace-with-held-native-promise" {
							holder = held
						}
						mark("original-public-delta-action")
						if spec.Program == "caller-abort" {
							cancel(errors.New(spec.CallerAbortReason))
						}
						if spec.Program == "return" {
							f, e := iterator.Return(fresh, modelyield.QueryValue{ReturnValue: raw(spec.ReturnValue)})
							if e != nil {
								return e
							}
							return consume(fresh, f, "return")
						}
						if spec.Program == "throw" {
							originalThrow, e := apipresentation.NewOriginalError(raw(spec.ThrowMessage))
							if e != nil {
								return e
							}
							f, e := iterator.Throw(fresh, originalThrow)
							if e != nil {
								return e
							}
							return consume(fresh, f, "throw")
						}
						return send(fresh, "release-original-ending", func(s *cooperative.Scope) error {
							nextCalls++
							f, e := iterator.Next(s)
							if e != nil {
								return e
							}
							return consume(s, f, "next")
						})
					}
					nextCalls++
					f, e := iterator.Next(fresh)
					if e != nil {
						return e
					}
					return consume(fresh, f, "next")
				})
				return e
			}
			start := func(s *cooperative.Scope, it *modelyield.QueryIterator, c *modelyield.ResourceControl) (*ownerasync.OwnerFuture[struct{}], error) {
				iterator, controls = it, c
				nextCalls = 1
				f, e := it.Next(s)
				if e != nil {
					return nil, e
				}
				return finished, consume(s, f, "next")
			}
			duration := modelaccounting.SuccessDuration{NowMillis: func() float64 { return clock }, GetPermission: func(*cooperative.Scope) (*ownerasync.OwnerFuture[protocol.JSONObject], error) {
				name := "initial-fulfilled"
				if holder == held {
					name = "original-held"
				}
				getters = append(getters, map[string]any{"holder": name, "currentUnixMillis": clock})
				return holder, nil
			}}
			policy, e := modelyield.StreamErrorPolicyFromOriginalEnvironment(map[string]string{"CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK": os.Getenv("CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK"), "CLAUDE_STREAM_TRANSIENT_RETRY_MAX": os.Getenv("CLAUDE_STREAM_TRANSIENT_RETRY_MAX"), "CLAUDE_ENABLE_STREAM_WATCHDOG": os.Getenv("CLAUDE_ENABLE_STREAM_WATCHDOG")})
			if e != nil {
				return e
			}
			_, failure := modelyield.RunQueued(caller, original, modelyield.QueuedDependencies{Session: session, StreamClock: &modelyield.ProducerClock{NowMillis: func() float64 { return clock }}, StreamPolicy: policy, AgentContext: binding, SuccessDuration: &duration, Start: start, Request: requestretry.Dependencies{StreamOwner: owner, StreamScope: scope, UUID: uuid, Now: func() time.Time { return time.UnixMilli(int64(clock)) }, Random: func() float64 { return 0 }, Sleep: func(context.Context, float64) error {
				return errors.New("unported retry in original no-retry duration program")
			}}})
			before := accounting(scope)
			var immediate, after any
			if holder == held {
				clock = spec.ClockAtPermissionRelease
				if e := held.Resolve(scope, permission); e != nil {
					return e
				}
				immediate = accounting(scope)
				if _, e := held.Await(scope); e != nil {
					return e
				}
				after = accounting(scope)
			}
			present, aborted, _ := controls.State(scope)
			request := map[string]any{"ordinal": ordinal, "program": spec.Program, "getterCalls": getters, "beforePermissionRelease": before, "afterImmediatePermissionRelease": immediate, "afterOriginalPromiseReaction": after, "finalAccounting": accounting(scope), "settlements": settlements, "yielded": yielded, "caller": map[string]any{"aborted": caller.Err() != nil}, "sdk": map[string]any{"present": present, "aborted": aborted}}
			if failure != nil {
				request["producerFailure"] = failure.Error()
			}
			requests = append(requests, request)
		}
		fmt.Println(string(raw(map[string]any{"kind": "result", "pid": os.Getpid(), "home": home, "originalID": in.Original.ID, "requests": requests, "values": allValues, "timeline": timeline, "finalAccounting": accounting(scope), "termination": "explicit finite Go original query consumer; not full CLI/natural drain"})))
		return nil
	})
	return e
}
