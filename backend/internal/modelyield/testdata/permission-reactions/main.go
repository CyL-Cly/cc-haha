package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/apipresentation"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelyield"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Original struct {
	ID, Program, HostPolicy string
	Clock                   struct{ UnixMillis float64 }
	Production              struct {
		HistorySources                         []string
		SystemSources                          []json.RawMessage
		Thinking                               requestcontext.Thinking
		Tools                                  []protocol.JSONObject
		SDKBetas                               []string
		SessionID, DeviceID, PermissionProfile string
		Options                                requestcontext.Options
	}
	Permission                                                        struct{ Initial, AtPublicMessageDelta, HeldSettlement, ErrorMessage string }
	ClockAtRetryYield, ClockAtPublicDelta, ClockAtAfterProducerSettle float64
	InitialLastMainRequestID                                          *string
	MaxRetries                                                        int
}
type Input struct {
	Original            Original
	Home, Origin, Guard string
}

func raw(v any) json.RawMessage {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return b
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
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(2)
	}
}

//go:embed original-profiles.json
var originalProfiles []byte

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
	var inputFields map[string]json.RawMessage
	if e = json.Unmarshal(line, &inputFields); e != nil {
		return e
	}
	for key := range inputFields {
		if key != "original" && key != "home" && key != "origin" && key != "guard" {
			return errors.New("unknown original fixture input")
		}
	}
	var allowed []json.RawMessage
	if e = json.Unmarshal(originalProfiles, &allowed); e != nil {
		return e
	}
	given, e := protocol.ParseAndStringifyJavaScriptJSON(inputFields["original"])
	if e != nil {
		return e
	}
	matched := false
	for _, value := range allowed {
		normalized, err := protocol.ParseAndStringifyJavaScriptJSON(value)
		if err != nil {
			return err
		}
		if bytes.Equal(given, normalized) {
			matched = true
			break
		}
	}
	if !matched {
		return errors.New("frozen original input only")
	}
	if in.Home != os.Getenv("HOME") || in.Home != os.Getenv("USERPROFILE") || !strings.HasPrefix(filepath.Base(in.Home), "cc-haha-permission-go-") {
		return errors.New("private allocation provenance before filesystem")
	}
	home, e := filepath.EvalSymlinks(in.Home)
	cwd, _ := os.Getwd()
	u, ue := url.Parse(in.Origin)
	if e != nil || in.Home != home || cwd != home || home != os.Getenv("HOME") || home != os.Getenv("USERPROFILE") || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(home, ".claude") || os.Getenv("BUN_OPTIONS") != "--no-env-file" || os.Getenv("CC_HAHA_PERMISSION_SOURCE") != "1" || os.Getenv("TZ") != "UTC" || os.Getenv("NODE_ENV") != "production" || os.Getenv("ANTHROPIC_BASE_URL") != in.Origin || os.Getenv("ANTHROPIC_API_KEY") != "offline-fixture-key" || ue != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" {
		return errors.New("explicit canonical private original permission input required")
	}
	if !strings.HasPrefix(filepath.Base(home), "cc-haha-permission-go-") {
		return errors.New("parent allocation provenance required")
	}
	if in.Guard != "" {
		return errors.New("unsupported original guard request")
	}
	owner := cooperative.New()
	defer owner.Close()
	session, e := modelaccounting.NewOwnedSession(owner, in.Original.InitialLastMainRequestID)
	if e != nil {
		return e
	}
	binding, _ := modelaccounting.NewMainContext(owner)
	ignored, _ := ownerasync.NewIgnoredRejections(owner)
	clock := in.Original.Clock.UnixMillis
	originalError := errors.New(in.Original.Permission.ErrorMessage)
	var getters, settlements, snapshots []map[string]any
	var yielded []json.RawMessage
	e = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		account := func(s *cooperative.Scope) protocol.JSONObject {
			v, _ := session.Snapshot(s)
			d, _ := session.DurationSnapshot(s)
			v.Assign(d)
			return v
		}
		snapshot := func(name string) {
			snapshots = append(snapshots, map[string]any{"name": name, "clock": clock, "accounting": account(scope)})
		}
		env := map[string]string{}
		for _, k := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY", "CLAUDE_CODE_SIMPLE", "CLAUDE_CODE_MAX_RETRIES"} {
			env[k] = os.Getenv(k)
		}
		original := requestretry.Input{Context: requestcontext.Context{SystemPrompt: in.Original.Production.SystemSources, Tools: in.Original.Production.Tools, Thinking: in.Original.Production.Thinking, Options: in.Original.Production.Options, Host: requestcontext.Host{Profile: requestcontext.DirectRelayProfile, Env: env, DeviceID: in.Original.Production.DeviceID, SessionID: in.Original.Production.SessionID, SDKBetas: in.Original.Production.SDKBetas}}, Runtime: provider.SourceRuntime{Platform: "darwin", Arch: "arm64", Version: "v24.3.0"}}
		for _, source := range in.Original.Production.HistorySources {
			var m protocol.JSONObject
			if e := m.UnmarshalJSON([]byte(source)); e != nil {
				return e
			}
			original.Context.History = append(original.Context.History, m)
		}
		fast := false
		original.ResolvedFastMode = &fast
		permission := protocol.NewJSONObject(protocol.JSONField{Name: "mode", Value: raw("default")})
		held, _ := ownerasync.NewOwnerFuture[protocol.JSONObject](owner)
		makeHolder := func(kind string) (*ownerasync.OwnerFuture[protocol.JSONObject], error) {
			if kind == "held" {
				return held, nil
			}
			if kind == "null-return" || kind == "throw" {
				return nil, nil
			}
			p, _ := ownerasync.NewOwnerFuture[protocol.JSONObject](owner)
			switch kind {
			case "fulfilled":
				_ = p.Resolve(scope, permission)
			case "rejected":
				_ = p.Reject(scope, originalError)
			case "resolve-null":
				_ = p.Resolve(scope, protocol.JSONObject{})
			case "empty-object":
				_ = p.Resolve(scope, protocol.NewJSONObject())
			default:
				return nil, errors.New("unsupported original holder kind")
			}
			return p, nil
		}
		kind := in.Original.Permission.Initial
		holder, e := makeHolder(kind)
		if e != nil {
			return e
		}
		ids := map[*ownerasync.OwnerFuture[protocol.JSONObject]]int{}
		duration := modelaccounting.SuccessDuration{Ignored: ignored, NowMillis: func() float64 { return clock }, GetPermission: func(*cooperative.Scope) (*ownerasync.OwnerFuture[protocol.JSONObject], error) {
			if _, ok := ids[holder]; !ok {
				ids[holder] = len(ids) + 1
			}
			getters = append(getters, map[string]any{"kind": kind, "promiseID": ids[holder], "currentClock": clock, "nativePromise": holder != nil})
			if kind == "throw" {
				return nil, originalError
			}
			return holder, nil
		}}
		finished, _ := ownerasync.NewOwnerFuture[struct{}](owner)
		var iterator *modelyield.QueryIterator
		deltaApplied := false
		valueJSON := func(v modelyield.QueryValue) json.RawMessage {
			if v.Yield == nil {
				return v.ReturnValue
			}
			if v.Yield.Notice != nil {
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
		var consume func(*cooperative.Scope, *ownerasync.OwnerFuture[modelyield.QueryResult]) error
		consume = func(s *cooperative.Scope, f *ownerasync.OwnerFuture[modelyield.QueryResult]) error {
			_, e := f.Then(s, func(next *cooperative.Scope, r modelyield.QueryResult, failure error) error {
				if failure != nil {
					settlements = append(settlements, map[string]any{"status": "rejected", "error": map[string]any{"message": failure.Error(), "sameOriginalError": failure == originalError}})
					return finished.Resolve(next, struct{}{})
				}
				v := protocol.NewJSONObject()
				if r.Defined {
					v.Set("value", valueJSON(r.Value))
				}
				v.Set("done", raw(r.Done))
				settlements = append(settlements, map[string]any{"status": "fulfilled", "result": v})
				if r.Done {
					return finished.Resolve(next, struct{}{})
				}
				source := valueJSON(r.Value)
				yielded = append(yielded, source)
				if r.Value.Yield != nil && r.Value.Yield.Notice != nil && strings.Contains(in.Original.Program, "public-system-api-error") {
					clock = in.Original.ClockAtRetryYield
				}
				if r.Value.Yield != nil && r.Value.Yield.Update != nil {
					var event protocol.JSONObject
					_ = event.UnmarshalJSON(r.Value.Yield.Update.Event)
					if event.String("type") == "message_delta" && !deltaApplied {
						deltaApplied = true
						clock = in.Original.ClockAtPublicDelta
						if in.Original.Permission.AtPublicMessageDelta != "keep" {
							kind = in.Original.Permission.AtPublicMessageDelta
							var e error
							holder, e = makeHolder(kind)
							if e != nil {
								return e
							}
						}
						snapshot("after-original-public-delta-action")
					}
				}
				n, e := iterator.Next(next)
				if e != nil {
					return e
				}
				return consume(next, n)
			})
			return e
		}
		policy, e := modelyield.StreamErrorPolicyFromOriginalEnvironment(map[string]string{"CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK": "1", "CLAUDE_STREAM_TRANSIENT_RETRY_MAX": "0", "CLAUDE_ENABLE_STREAM_WATCHDOG": "0"})
		if e != nil {
			return e
		}
		snapshot("before-original-next")
		_, failure := modelyield.RunQueued(context.Background(), original, modelyield.QueuedDependencies{Session: session, AgentContext: binding, SuccessDuration: &duration, StreamClock: &modelyield.ProducerClock{NowMillis: func() float64 { return clock }}, StreamPolicy: policy, Start: func(s *cooperative.Scope, it *modelyield.QueryIterator, _ *modelyield.ResourceControl) (*ownerasync.OwnerFuture[struct{}], error) {
			iterator = it
			f, e := it.Next(s)
			if e != nil {
				return nil, e
			}
			return finished, consume(s, f)
		}, Request: requestretry.Dependencies{StreamOwner: owner, StreamScope: scope, UUID: uuid, Now: func() time.Time { return time.UnixMilli(int64(clock)) }, Random: func() float64 { return 0 }, Sleep: func(ctx context.Context, ms float64) error {
			_, err := scope.AwaitBytes(cooperative.RetryTimer, func(context.Context) ([]byte, error) {
				timer := time.NewTimer(time.Duration(ms * float64(time.Millisecond)))
				defer timer.Stop()
				select {
				case <-timer.C:
					return nil, nil
				case <-ctx.Done():
					return nil, context.Cause(ctx)
				}
			})
			return err
		}}})
		snapshot("after-original-producer-settlement")
		if holder == held {
			fmt.Println(string(raw(map[string]any{"kind": "control", "action": "original-deferred-settle", "id": in.Original.ID})))
			var receipt struct {
				Action, ID string
				Done       bool
			}
			b, e := reader.ReadBytes('\n')
			if e != nil {
				return e
			}
			if e = json.Unmarshal(b, &receipt); e != nil {
				return e
			}
			if receipt.Action != "original-deferred-settle" || receipt.ID != in.Original.ID || !receipt.Done {
				return errors.New("original external receipt mismatch")
			}
			clock = in.Original.ClockAtAfterProducerSettle
			_ = held.Resolve(scope, permission)
			snapshot("immediately-after-original-resolve")
			if _, e = held.Await(scope); e != nil {
				return e
			}
			snapshot("after-await-same-original-promise")
		}
		result := map[string]any{"kind": "result", "pid": os.Getpid(), "home": home, "originalID": in.Original.ID, "getters": getters, "settlements": settlements, "yielded": yielded, "snapshots": snapshots, "finalAccounting": account(scope), "termination": "Go component returns with native unhandled runtime boundary not implemented; original direct query consumer"}
		if failure != nil {
			result["producerFailure"] = failure.Error()
		}
		logs, _ := session.QueryLogSnapshot(scope)
		result["queryLogQueue"] = logs
		pending, _ := ignored.Pending(scope)
		notices := []map[string]any{}
		seen := map[*ownerasync.PromiseIdentity]bool{}
		for _, n := range pending {
			notices = append(notices, map[string]any{"site": n.Site, "sameOriginalError": n.Original == originalError, "distinctFromHolder": n.Promise != n.Holder, "duplicatePromise": seen[n.Promise], "message": n.Original.Error()})
			seen[n.Promise] = true
		}
		result["pendingIgnoredRejections"] = notices
		result["runtimeDispatchImplemented"] = false
		fmt.Println(string(raw(result)))
		return nil
	})
	return e
}

var _ = apipresentation.DomainFromOriginalEnv
