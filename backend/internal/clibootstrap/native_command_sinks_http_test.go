package clibootstrap

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/analytics"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/errorlog"
	"github.com/NanmiCoder/cc-haha/backend/internal/plugincli"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeCommandQueuedAndHandlerEventsReachActualFirstPartyHTTP(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(fmt.Sprint("disabled-", disabled), func(t *testing.T) {
			f := newNativePluginFixture(t)
			debugFile := filepath.Join(f.config, "command-debug.txt")
			f.n.Environment["USER_TYPE"] = "external"
			f.n.Environment["NODE_ENV"] = "production"
			f.n.Environment["DEBUG"] = "1"
			f.n.Environment["CLAUDE_CODE_DEBUG_LOGS_DIR"] = debugFile
			f.n.Environment["GITHUB_ACTIONS"] = "true"
			f.n.Environment["GITHUB_ACTOR_ID"] = "owned-actor"
			f.n.Environment["GITHUB_REPOSITORY_ID"] = "owned-repository"
			f.n.Environment["GITHUB_REPOSITORY_OWNER_ID"] = "owned-owner"
			if !disabled {
				delete(f.n.Environment, "DISABLE_TELEMETRY")
			}
			batches := make(chan []byte, 2)
			var heads, posts atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodHead {
					heads.Add(1)
					w.WriteHeader(http.StatusNoContent)
					return
				}
				body, err := io.ReadAll(r.Body)
				if err != nil || r.Method != http.MethodPost || r.URL.Path != "/api/event_logging/batch" {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				posts.Add(1)
				batches <- body
				w.WriteHeader(http.StatusOK)
			}))
			defer peer.Close()
			f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
			m, err := NewMainCommand(MainCommandOptions{Arguments: []string{"plugin", "list", "--json"}, Native: f.n, Platform: runtime.GOOS, PID: os.Getpid(), Version: "1.0.0-private", NonInteractive: true, Output: io.Discard, Error: io.Discard,
				AnalyticsHTTP: func(ctx context.Context, input analytics.Request) (analytics.Response, error) {
					if input.URL != "https://api.anthropic.com/api/event_logging/batch" || input.Timeout != 10*time.Second || input.Headers.Get("x-service-name") != "claude-code" {
						return analytics.Response{}, fmt.Errorf("foreign first-party HTTP preparation: %s %s", input.URL, input.Timeout)
					}
					request, err := http.NewRequestWithContext(ctx, http.MethodPost, peer.URL+"/api/event_logging/batch", bytes.NewReader(input.Body))
					if err != nil {
						return analytics.Response{}, err
					}
					request.Header = input.Headers.Clone()
					response, err := peer.Client().Do(request)
					if err != nil {
						return analytics.Response{}, err
					}
					defer response.Body.Close()
					body, err := io.ReadAll(response.Body)
					return analytics.Response{Status: response.StatusCode, Headers: response.Header.Clone(), Body: body}, err
				}})
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			var result plugincli.Result
			if err := m.Run(ctx, func(scope *cooperative.Scope) error {
				metadata := protocol.NewJSONObject(protocol.JSONField{Name: "value", Value: rawMain(1)})
				if err := m.commandEvents.Log(scope, analytics.Event{Name: "owned_queued_sync", Metadata: metadata}); err != nil {
					return err
				}
				metadata.Set("value", rawMain(41))
				async, err := m.commandEvents.LogAsync(scope, analytics.Event{Name: "owned_queued_async", Metadata: metadata})
				if err != nil {
					return err
				}
				metadata.Set("value", rawMain(42))
				_, err = async.Await(scope)
				m.commandErrors.Log(errorlog.Error{Name: "Error", Message: "owned-before-attachment"})
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if err := m.BeginNativeAPIPreconnect(ctx); err != nil {
				t.Fatal(err)
			}
			if err := m.Run(ctx, func(scope *cooperative.Scope) error {
				var err error
				result, err = m.PluginCommand(ctx, scope, plugincli.Command{Action: "list", JSON: true})
				if err != nil {
					return err
				}
				// Exercise the already-existing actual exporter boundary. Normal
				// cliOk/process.exit does not gain an exporter flush from this test.
				return m.commandAnalytics.Flush(scope)
			}); err != nil {
				t.Fatal(err)
			}
			// This fixture declares a marketplace and enabledPlugin but has no
			// installed registry entry: the actual JSON installed-list is empty.
			if result.ExitCode != 0 || result.Stderr != "" || strings.TrimSpace(result.Stdout) != "[]" {
				t.Fatal("actual handler behavior changed", result)
			}
			if m.commandAnalyticsSink == nil || m.commandErrorSink == nil {
				t.Fatal("privacy suppressed real sink attachment")
			}
			if disabled {
				if posts.Load() != 0 {
					t.Fatal("disabled first-party consumer performed actual HTTP", posts.Load())
				}
			} else {
				select {
				case raw := <-batches:
					var batch struct {
						Events []protocol.JSONObject `json:"events"`
					}
					if err := json.Unmarshal(raw, &batch); err != nil {
						t.Fatal(err, string(raw))
					}
					names := []string{}
					for _, event := range batch.Events {
						var data protocol.JSONObject
						_ = json.Unmarshal(event.Get("event_data"), &data)
						name := data.String("event_name")
						names = append(names, name)
						if data.String("session_id") != m.commandSessionID || data.String("model") == "" {
							t.Fatal("actual native metadata producer missing", string(raw))
						}
						var env protocol.JSONObject
						_ = json.Unmarshal(data.Get("env"), &env)
						var github protocol.JSONObject
						_ = json.Unmarshal(env.Get("github_actions_metadata"), &github)
						if github.String("actor_id") != "owned-actor" {
							t.Fatal("actual Github Actions producer lost", string(raw))
						}
						if strings.HasPrefix(name, "owned_queued_") {
							decoded, err := base64.StdEncoding.DecodeString(data.String("additional_metadata"))
							if err != nil || string(decoded) != `{"value":42}` {
								t.Fatal("public queue copied metadata before actual attachment", err, string(decoded))
							}
						}
						if name == "tengu_reset_pro_to_opus_default" {
							decoded, err := base64.StdEncoding.DecodeString(data.String("additional_metadata"))
							if err != nil || string(decoded) != `{"skipped":true}` {
								t.Fatal("actual empty-credential migration event differs from whole Source", err, string(decoded))
							}
						}
					}
					if !reflect.DeepEqual(names, []string{"owned_queued_sync", "owned_queued_async", "tengu_reset_pro_to_opus_default", "tengu_plugin_list_command"}) || posts.Load() != 1 {
						t.Fatal("real first-party handler routing/order changed", names, posts.Load())
					}
				default:
					t.Fatal("actual exporter Flush completed without a physical batch")
				}
			}
			m.Close()
			debug, err := os.ReadFile(debugFile)
			if err != nil || strings.Count(string(debug), "Error log sink initialized") != 1 || !strings.Contains(string(debug), "owned-before-attachment") {
				t.Fatal("actual queued error/debug consumer lost on Close", err, string(debug))
			}
			if heads.Load() != 1 {
				t.Fatal("preAction's original HEAD producer changed", heads.Load())
			}
		})
	}
}

func TestNativeCommandCloseCancelsAndJoinsActualHeldFirstPartyHTTP(t *testing.T) {
	f := newNativePluginFixture(t)
	delete(f.n.Environment, "DISABLE_TELEMETRY")
	f.n.Environment["USER_TYPE"] = "external"
	f.n.Environment["NODE_ENV"] = "production"
	started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		select {
		case <-r.Context().Done():
			close(canceled)
		case <-release:
		}
	}))
	defer peer.Close()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	m, err := NewMainCommand(MainCommandOptions{Arguments: []string{"plugin", "list", "--json"}, Native: f.n, Platform: runtime.GOOS, PID: os.Getpid(), Output: io.Discard, Error: io.Discard,
		AnalyticsHTTP: func(ctx context.Context, input analytics.Request) (analytics.Response, error) {
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, peer.URL+"/api/event_logging/batch", bytes.NewReader(input.Body))
			if err != nil {
				return analytics.Response{}, err
			}
			request.Header = input.Headers.Clone()
			response, err := peer.Client().Do(request)
			if err != nil {
				return analytics.Response{}, err
			}
			defer response.Body.Close()
			return analytics.Response{Status: response.StatusCode}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
		m.Close()
	}()
	if err := m.BeginNativeAPIPreconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.Run(context.Background(), func(scope *cooperative.Scope) error {
		if _, err := m.PluginCommand(context.Background(), scope, plugincli.Command{Action: "list", JSON: true}); err != nil {
			return err
		}
		if err := scope.AwaitFulfilled(); err != nil {
			return err
		}
		return m.commandAnalytics.BeginFlush(scope)
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("real first-party HTTP never started")
	}
	closed := make(chan struct{})
	go func() { m.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("actual held exporter blocked command Close")
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("Close returned without canceling actual held HTTP")
	}
	if count, _ := m.nativeInitRefs.Snapshot(); count != 0 {
		t.Fatal("actual exporter worker/reaction remained after Close", count)
	}
}
