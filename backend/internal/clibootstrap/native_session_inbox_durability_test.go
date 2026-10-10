package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioninbox"
)

func TestActualBusyInboxDurableReceiptReplayAndColdResume(t *testing.T) {
	for _, profile := range []struct{ replay, disabled bool }{{false, false}, {true, false}, {true, true}} {
		replay := profile.replay
		t.Run(fmt.Sprint("replay=", replay, ",disabled=", profile.disabled), func(t *testing.T) {
			f := newNativePluginFixture(t)
			f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
			file := filepath.Join(f.n.CWD, "inbox-read.txt")
			f.write(t, file, "PRIVATE_INBOX_READ")
			f.n.Environment["ANTHROPIC_API_KEY"] = "private-inbox-fake-key"
			f.n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "0"
			f.n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
			var requests atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if requests.Add(1) == 1 {
					fmt.Fprint(w, webWire("fixture-model", []protocol.JSONObject{webObject(fmt.Sprintf(`{"type":"tool_use","id":"inbox-read","name":"Read","input":{"file_path":%q}}`, file))}))
				} else {
					if !bytes.Contains(body, []byte("busy-durable-message")) || !bytes.Contains(body, []byte("PRIVATE_INBOX_READ")) {
						t.Errorf("actual continuation omitted queued prompt or tool result: %.2000s", body)
					}
					emitPlanFixture(w, 2, "", "BUSY_INBOX_OK")
				}
			}))
			defer peer.Close()
			f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
			messageID := "busy-durable-message"
			uuid := sessioninbox.MessageUUID(quoteMain(messageID))
			request := webObject(`{"subtype":"enqueue_session_message","message_id":"busy-durable-message","sender_session_id":"peer","text":"Please acknowledge progress"}`)
			disabled := false
			args := PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "Actual busy native inbox", Tools: []string{"Read"}, MaxTurns: 4, PermissionMode: "bypassPermissions", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState(), Replay: replay, SessionPersistenceDisabled: profile.disabled}
			var main *MainPrint
			var frames []protocol.JSONObject
			accepted, yieldedPending := false, false
			main, err := NewMainPrint(MainPrintOptions{Arguments: args, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard,
				OnQueryYield: func(scope *cooperative.Scope, yield *queryloop.Yield) error {
					if yield.Message == nil {
						return nil
					}
					if yield.Message.Type() == "assistant" && !accepted {
						accepted = true
						result, e := main.Host.AcceptSessionMessage(scope, request)
						if e != nil {
							return e
						}
						if result.String("status") != "queued" {
							return fmt.Errorf("unexpected admission: %s", rawMain(result))
						}
					}
					if yield.Message.Type() == "attachment" && taskObject(t, yield.Message.Record.Get("attachment")).String("source_uuid") == uuid {
						pending, e := main.Controller.SessionMessagePendingOnOwner(scope, uuid)
						if e != nil {
							return e
						}
						yieldedPending = pending
					}
					return nil
				}, OnOutput: func(data []byte) error {
					frame := taskObject(t, data)
					frames = append(frames, frame)
					if frame.String("subtype") == "session_message_receipt" && !profile.disabled {
						path := filepath.Join(f.config, "projects", projectpath.Sanitize(f.n.CWD), main.Controller.SessionID()+".jsonl")
						data, e := os.ReadFile(path)
						if e != nil || !bytes.Contains(data, []byte(`"tool_use_id":"inbox-read"`)) {
							t.Errorf("consumed receipt before actual tool-result append+flush: %v", e)
						}
					}
					return nil
				}})
			if err != nil {
				t.Fatal(err)
			}
			defer main.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			if e := main.StartSession(ctx); e != nil {
				t.Fatal(e)
			}
			if e := main.RunTurn(ctx, webObject(`{"type":"user","message":{"role":"user","content":"Read inbox-read.txt then respond"}}`)); e != nil {
				t.Fatal(e)
			}
			if e := main.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				duplicate, e := main.Host.AcceptSessionMessage(scope, request)
				if e != nil {
					return e
				}
				if duplicate.String("status") != "consumed" || string(duplicate.Get("duplicate")) != "true" {
					return fmt.Errorf("warm consumed inbox was redelivered: %s", rawMain(duplicate))
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			if e := main.ShutdownSession(ctx); e != nil {
				t.Fatal(e)
			}
			if !yieldedPending {
				t.Error("attachment was consumed before the actual SDK consumer")
			}
			consumed, replays := 0, 0
			for _, frame := range frames {
				if frame.String("subtype") == "session_message_receipt" {
					consumed++
				}
				if frame.String("type") == "user" && frame.String("uuid") == uuid && string(frame.Get("isReplay")) == "true" {
					replays++
					if taskObject(t, frame.Get("message")).String("content") != stringMustJSONText(sessioninbox.FormatPrompt(quoteMain("peer"), quoteMain(messageID), quoteMain("Please acknowledge progress"))) || string(frame.Get("parent_tool_use_id")) != "null" || frame.String("timestamp") == "" {
						t.Fatal(frame)
					}
				}
			}
			if consumed != 1 || replays != map[bool]int{false: 0, true: 1}[replay] || requests.Load() != 2 {
				t.Fatalf("receipt/replay/requests=%d/%d/%d", consumed, replays, requests.Load())
			}
			for _, record := range main.Controller.History() {
				if record.String("type") == "attachment" && taskObject(t, record.Get("attachment")).String("source_uuid") == uuid && record.String("uuid") != uuid {
					t.Error("canonical attachment UUID differs from delivery UUID")
				}
			}
			if profile.disabled {
				path := filepath.Join(f.config, "projects", projectpath.Sanitize(f.n.CWD), main.Controller.SessionID()+".jsonl")
				if _, e := os.Stat(path); !errors.Is(e, os.ErrNotExist) {
					t.Fatalf("disabled persistence wrote a transcript: %v", e)
				}
				return
			}
			args.Resume = main.Controller.SessionID()
			resumed, e := NewMainPrint(MainPrintOptions{Arguments: args, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
			if e != nil {
				t.Fatal(e)
			}
			defer resumed.Close()
			if e = resumed.StartSession(ctx); e != nil {
				t.Fatal(e)
			}
			if e = resumed.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				duplicate, e := resumed.Host.AcceptSessionMessage(scope, request)
				if e != nil {
					return e
				}
				if duplicate.String("status") != "queued" || string(duplicate.Get("duplicate")) != "false" {
					return fmt.Errorf("cold resume must preserve original external queued-attachment filtering: %s", rawMain(duplicate))
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			if requests.Load() != 2 {
				t.Fatal("cold queued control without start_if_idle started inference")
			}
		})
	}
}

func TestActualIdleInboxKeepsReceiptPendingDuringCanonicalCloseAndRejectsForeignOwner(t *testing.T) {
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
	f.n.Environment["ANTHROPIC_API_KEY"] = "private-idle-inbox-key"
	f.n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "0"
	f.n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
	var requests, receipts, replays atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		emitPlanFixture(w, 1, "", "IDLE_DURABLE_INBOX_OK")
	}))
	defer peer.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	request := webObject(`{"subtype":"enqueue_session_message","message_id":"idle-close-delivery","sender_session_id":"peer","text":"Await canonical close","start_if_idle":true}`)
	uuid := sessioninbox.MessageUUID(quoteMain("idle-close-delivery"))
	entered, release := make(chan struct{}), make(chan struct{})
	var onceEnter, onceRelease sync.Once
	unblock := func() { onceRelease.Do(func() { close(release) }) }
	defer unblock()
	disabled := false
	main, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "Actual canonical close gate", Replay: true, Tools: []string{"Read"}, MaxTurns: 2, PermissionMode: "bypassPermissions", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(inboxControl("idle-close", request)), Output: io.Discard, Error: io.Discard,
		OnCanonicalTranscriptMutation: func(path string) error {
			data, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			if bytes.Contains(data, []byte(`"uuid":"`+uuid+`"`)) {
				onceEnter.Do(func() { close(entered); <-release })
			}
			return nil
		}, OnOutput: func(data []byte) error {
			frame := taskObject(t, data)
			if frame.String("subtype") == "session_message_receipt" {
				receipts.Add(1)
			}
			if frame.String("type") == "user" && frame.String("uuid") == uuid {
				replays.Add(1)
			}
			return nil
		}})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { unblock(); main.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	finished := make(chan error, 1)
	go func() { _, e := main.Run(ctx); finished <- e }()
	select {
	case <-entered:
	case e := <-finished:
		t.Fatalf("real physical close was not entered: %v", e)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if receipts.Load() != 0 || requests.Load() != 0 {
		t.Fatal("consumer passed the real canonical flush before close returned", receipts.Load(), requests.Load())
	}
	read, e := main.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		pending, e := main.Controller.SessionMessagePendingOnOwner(scope, uuid)
		if e != nil {
			return e
		}
		if !pending {
			return errors.New("idle input was consumed before Record/Flush completion")
		}
		duplicate, e := main.Host.AcceptSessionMessage(scope, request)
		if e != nil {
			return e
		}
		if duplicate.String("status") != "queued" || string(duplicate.Get("duplicate")) != "true" {
			return fmt.Errorf("pending duplicate changed while flush held: %s", rawMain(duplicate))
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	if e := read.Wait(); e != nil {
		t.Fatal(e)
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if e := foreign.Run(ctx, func(scope *cooperative.Scope) error {
		if _, e := main.Controller.SessionMessagePendingOnOwner(scope, uuid); !errors.Is(e, cooperative.ErrRestricted) {
			return errors.New("foreign pending read accepted")
		}
		if e := main.Controller.ConsumeSessionMessageOnOwner(scope, uuid); !errors.Is(e, cooperative.ErrRestricted) {
			return errors.New("foreign consumption accepted")
		}
		if _, e := main.Controller.ReplayUserMessagesOnOwner(scope); !errors.Is(e, cooperative.ErrRestricted) {
			return errors.New("foreign replay configuration accepted")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	unblock()
	if e := <-finished; e != nil {
		t.Fatal(e)
	}
	if receipts.Load() != 1 || requests.Load() != 1 || replays.Load() != 0 {
		t.Fatal("actual resumed flush did not consume once before inference", receipts.Load(), requests.Load())
	}
	args := main.options.Arguments
	args.Resume = main.Controller.SessionID()
	resumed, e := NewMainPrint(MainPrintOptions{Arguments: args, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if e != nil {
		t.Fatal(e)
	}
	defer resumed.Close()
	if e := resumed.StartSession(ctx); e != nil {
		t.Fatal(e)
	}
	if e := resumed.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		duplicate, e := resumed.Host.AcceptSessionMessage(scope, request)
		if e != nil {
			return e
		}
		if duplicate.String("status") != "consumed" || string(duplicate.Get("duplicate")) != "true" {
			return fmt.Errorf("cold resume lost original idle user UUID: %s", rawMain(duplicate))
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if requests.Load() != 1 {
		t.Fatal("cold idle duplicate started a model")
	}
}

func stringMustJSONText(data json.RawMessage) string {
	var value string
	_ = json.Unmarshal(data, &value)
	return value
}
