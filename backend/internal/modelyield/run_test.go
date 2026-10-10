package modelyield

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"github.com/NanmiCoder/cc-haha/backend/internal/retrypolicy"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
)

func TestPhysicalCreationTerminalMatchesActualSource(t *testing.T) {
	raw, err := os.ReadFile("testdata/source_terminal.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden struct {
		Bun, Revision string
		Cases         []struct {
			Input            modelFixtureInput
			ExpectedTerminal string
			ExpectedNotices  []string
			SourceRawSHA256  string
		}
	}
	if err = json.Unmarshal(raw, &golden); err != nil {
		t.Fatal(err)
	}
	if golden.Bun != "1.3.14" || golden.Revision != "0d9b296af33f2b851fcbf4df3e9ec89751734ba4" || len(golden.Cases) != 2 {
		t.Fatal("missing actual pinned Source terminal evidence")
	}
	for _, item := range golden.Cases {
		t.Run(item.Input.Name, func(t *testing.T) {
			if len(item.SourceRawSHA256) != 64 {
				t.Fatal("missing original Source provenance")
			}
			var requests atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
			defer server.Close()
			originalBase := item.Input.Env["ANTHROPIC_BASE_URL"]
			item.Input.Home = t.TempDir()
			item.Input.Env["ANTHROPIC_BASE_URL"] = server.URL
			if item.Input.Name == "creation-malformed" {
				path := filepath.Join(item.Input.Home, "original-ca.pem")
				if err := os.WriteFile(path, []byte("private-invalid-pem-input\n"), 0600); err != nil {
					t.Fatal(err)
				}
				item.Input.Env["NODE_EXTRA_CA_CERTS"] = path
			}
			observation, err := observeModelFixture(item.Input)
			if err != nil {
				t.Fatal(err)
			}
			if requests.Load() != 0 || observation.Outcome != "success" || observation.UUIDCalls != 4 || len(observation.Yielded) != 3 || len(observation.Sleeps) != 2 {
				t.Fatalf("creation failure lost terminal/ordering: %+v; requests %d", observation, requests.Load())
			}
			if observation.Yielded[2] != item.ExpectedTerminal {
				t.Fatalf("actual Source terminal drift\nwant %s\ngot %s", item.ExpectedTerminal, observation.Yielded[2])
			}
			for index, notice := range observation.Notices {
				want := strings.ReplaceAll(item.ExpectedNotices[index], originalBase, "<allocated-origin>")
				got := strings.ReplaceAll(notice, server.URL, "<allocated-origin>")
				if got != want {
					t.Fatalf("actual Source notice drift\nwant %s\ngot %s", want, got)
				}
			}
			for index, value := range observation.HistoryBefore {
				if observation.HistoryAfter[index] != value {
					t.Fatal("terminal presentation changed original history")
				}
			}
		})
	}
}

func TestCreationConsumerStopsAndFailuresRetainOwnership(t *testing.T) {
	for _, mode := range []string{"first-notice", "last-notice", "terminal", "notice-error", "terminal-error", "notice-wrapped-error", "terminal-wrapped-error", "invalid-notice-action", "invalid-terminal-action", "abort-before", "abort-sleep", "invalid-domain", "invalid-host", "nil-consumer", "existing-consumer", "nil-clock", "nil-context"} {
		t.Run(mode, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, "original-ca.pem")
			if err := os.WriteFile(path, []byte("private-invalid-pem-input\n"), 0600); err != nil {
				t.Fatal(err)
			}
			input, err := contextFromFixture(fixtureInput{Env: map[string]string{"ANTHROPIC_BASE_URL": "https://127.0.0.1:1", "ANTHROPIC_API_KEY": "offline-original-key", "CLAUDE_CODE_DISABLE_FAST_MODE": "1", "CLAUDE_CODE_MAX_RETRIES": "2", "NODE_EXTRA_CA_CERTS": path}, Model: "deepseek-flash", MaxOutputTokens: 32000, DeviceID: "fixture-device", SessionID: "00000000-0000-4000-8000-000000000000", Runtime: provider.SourceRuntime{Platform: "darwin", Arch: "arm64", Version: "v24.3.0"}})
			if err != nil {
				t.Fatal(err)
			}
			state, _ := tlsconfig.NewState(os.ReadFile)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ids, notices, terminal := 0, 0, 0
			originalFailure := errors.New("consumer-owned failure")
			if strings.Contains(mode, "wrapped") {
				originalFailure = fmt.Errorf("consumer-owned wrapper: %w", requestretry.ErrConsumerStopped)
			}
			deps := Dependencies{Request: requestretry.Dependencies{TLSState: state, UUID: func() string { ids++; return fmt.Sprintf("id-%d", ids) }, Now: func() time.Time { return time.UnixMilli(1700000000000) }, Random: func() float64 { return 0 }, Sleep: func(context.Context, float64) error {
				if mode == "abort-sleep" {
					cancel()
					return &provider.SDKStreamError{Class: "APIUserAbortError", Message: "Request was aborted."}
				}
				return nil
			}}, Emit: func(value Yield) (requestretry.NoticeAction, error) {
				if value.Notice != nil {
					notices++
					if mode == "notice-error" || mode == "notice-wrapped-error" {
						return 0, originalFailure
					}
					if mode == "invalid-notice-action" {
						return 0, nil
					}
					if mode == "first-notice" || mode == "last-notice" && notices == 2 {
						return requestretry.Stop, nil
					}
				} else {
					terminal++
					if mode == "terminal-error" || mode == "terminal-wrapped-error" {
						return 0, originalFailure
					}
					if mode == "invalid-terminal-action" {
						return 0, nil
					}
					if mode == "terminal" {
						return requestretry.Stop, nil
					}
				}
				return requestretry.Resume, nil
			}}
			switch mode {
			case "abort-before":
				cancel()
			case "invalid-domain":
				input.Context.Host.Env["REMOTE_CONTAINERS"] = "1"
			case "invalid-host":
				input.Context.Thinking.Type = "enabled"
			case "nil-consumer":
				deps.Emit = nil
			case "existing-consumer":
				deps.Request.Notice = func(context.Context, retrypolicy.RetryNotice) (requestretry.NoticeAction, error) {
					return requestretry.Resume, nil
				}
			case "nil-clock":
				deps.Request.Now = nil
			case "nil-context":
				ctx = nil
			}
			result, err := Run(ctx, input, deps)
			if strings.HasSuffix(mode, "-error") {
				if err != originalFailure {
					t.Fatalf("consumer error identity changed: %v", err)
				}
			} else if strings.HasPrefix(mode, "invalid-") || strings.HasPrefix(mode, "nil-") || mode == "existing-consumer" {
				if err == nil {
					t.Fatal("unported host/consumer silently accepted")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(mode, "notice") && terminal != 0 {
				t.Fatal("notice stop/error allocated terminal")
			}
			if mode == "first-notice" && ids != 1 || mode == "last-notice" && ids != 2 || mode == "terminal" && ids != 4 {
				t.Fatalf("allocation continued beyond consumer: %d", ids)
			}
			if mode == "terminal" && (!result.Terminal || result.Reason != "consumer_stopped") {
				t.Fatal(result)
			}
			if mode == "abort-before" || mode == "abort-sleep" {
				if result.Reason != "caller_aborted" || terminal != 0 {
					t.Fatalf("abort was presented as terminal: %+v", result)
				}
			}
		})
	}
}

func TestSingleResponseConsumerClosesWithoutResubmitting(t *testing.T) {
	for _, mode := range []string{"stream-error", "stream-wrapped-error", "invalid-stream-action", "abort-body", "finish-stop", "finish-error", "finish-wrapped-error", "json-fault", "block-fault", "unsupported-creation"} {
		t.Run(mode, func(t *testing.T) {
			serverMode := "sequence"
			if mode == "stream-error" || mode == "stream-wrapped-error" || mode == "invalid-stream-action" || mode == "abort-body" {
				serverMode = "stop-stream"
			}
			server := startFixtureServer(t, serverMode, func(int) (int, string) {
				switch mode {
				case "json-fault":
					return 200, "event: message_start\ndata: {broken\n\n"
				case "block-fault":
					return 200, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"before block\"}}\n\n"
				case "unsupported-creation":
					return 500, `{"message":"original API failure"}`
				default:
					return 200, responseWire
				}
			})
			input, err := contextFromFixture(fixtureInput{Env: map[string]string{"ANTHROPIC_BASE_URL": server.origin, "ANTHROPIC_API_KEY": "offline-original-key", "CLAUDE_CODE_DISABLE_FAST_MODE": "1", "CLAUDE_CODE_MAX_RETRIES": "2"}, Model: "deepseek-flash", MaxOutputTokens: 32000, DeviceID: "fixture-device", SessionID: "00000000-0000-4000-8000-000000000000", Runtime: provider.SourceRuntime{Platform: "darwin", Arch: "arm64", Version: "v24.3.0"}})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			marker := errors.New("original stream consumer failure")
			if strings.Contains(mode, "wrapped") {
				marker = fmt.Errorf("original stream wrapper: %w", requestretry.ErrConsumerStopped)
			}
			ids, synthetic := 0, 0
			result, err := Run(ctx, input, Dependencies{Request: requestretry.Dependencies{
				UUID: func() string { ids++; return fmt.Sprintf("id-%d", ids) }, Now: func() time.Time { return time.UnixMilli(1700000000000) }, Random: func() float64 { return 0 }, Sleep: func(context.Context, float64) error { return nil },
			}, Emit: func(value Yield) (requestretry.NoticeAction, error) {
				if value.Notice != nil {
					return requestretry.Resume, nil
				}
				if value.Update.Envelope != nil {
					if value.Update.Envelope.APIError {
						synthetic++
					}
					if mode == "finish-stop" {
						return requestretry.Stop, nil
					}
					if mode == "finish-error" || mode == "finish-wrapped-error" {
						return 0, marker
					}
				} else {
					if mode == "stream-error" || mode == "stream-wrapped-error" {
						return 0, marker
					}
					if mode == "invalid-stream-action" {
						return 0, nil
					}
					if mode == "abort-body" {
						cancel()
					}
				}
				return requestretry.Resume, nil
			}})
			if mode == "stream-error" || mode == "stream-wrapped-error" || mode == "finish-error" || mode == "finish-wrapped-error" {
				if err != marker {
					t.Fatalf("stream callback identity changed: %v", err)
				}
			} else if mode == "abort-body" {
				if err != nil || result.Reason != "caller_aborted" {
					t.Fatalf("body abort became response/terminal: %+v %v", result, err)
				}
			} else if mode == "finish-stop" {
				if err != nil || result.Reason != "consumer_stopped" {
					t.Fatalf("finish stop lost: %+v %v", result, err)
				}
			} else if err == nil {
				t.Fatal("unsupported stream/creation silently accepted")
			}
			state := server.state()
			wantRequests := float64(1)
			if mode == "unsupported-creation" {
				wantRequests = 3
			}
			if state["requests"] != wantRequests || state["closedModelConnections"] != wantRequests || synthetic != 0 {
				t.Fatalf("error resubmitted/leaked/invented terminal: %v synthetic%d", state, synthetic)
			}
			if mode == "unsupported-creation" && ids != 2 {
				t.Fatalf("unsupported presentation allocated extra IDs: %d", ids)
			}
		})
	}
}
