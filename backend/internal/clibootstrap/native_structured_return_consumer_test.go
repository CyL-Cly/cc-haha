package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
)

func actualHeadlessEndSessionReturn(t *testing.T) (events []string, responses []protocol.JSONObject) {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual Source Bun required; end_session return regression cannot skip")
	}
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	temp := filepath.Join(home, "tmp")
	if err = os.Mkdir(temp, 0700); err != nil {
		t.Fatal(err)
	}
	fixture := os.Getenv("CC_HAHA_STRUCTURED_RETURN_FIXTURE")
	if fixture == "" {
		fixture, err = filepath.Abs("../sdkhost/testdata/structured-return.fixture.ts")
		if err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_STRUCTURED_RETURN_CONTRACT=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "ANTHROPIC_API_KEY=offline-fixture-key", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_SIMPLE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin", "TZ=UTC"}
	body, _ := json.Marshal(map[string]any{"home": home, "sourceRoot": root, "profiles": []any{}, "headless": true})
	cmd.Stdin = bytes.NewReader(body)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual Source end_session consumer: %v %.6000s", err, stderr.String())
	}
	t.Logf("actual Source public headless end_session capture: %s", data)
	var result struct {
		Headless struct {
			Events    []string
			Responses []protocol.JSONObject
		}
		Blocked []any
	}
	if err = json.Unmarshal(data, &result); err != nil || len(result.Blocked) != 0 {
		t.Fatalf("actual Source headless observation: %v %.6000s", err, data)
	}
	if !reflect.DeepEqual(result.Headless.Events, []string{"raw:entry", "raw:finally"}) || len(result.Headless.Responses) != 1 || result.Headless.Responses[0].String("request_id") != "actual-end" || result.Headless.Responses[0].String("subtype") != "success" {
		t.Fatalf("actual Source did not close original end_session producer: %.6000s", data)
	}
	return result.Headless.Events, result.Headless.Responses
}

func TestActualEndSessionWaitsForStructuredInputReturn(t *testing.T) {
	// Original Source public print consumer, not a hand-written break loop.
	sourceEvents, sourceResponses := actualHeadlessEndSessionReturn(t)
	for _, mode := range []string{"normal", "finally-error", "finally-yield"} {
		t.Run(mode, func(t *testing.T) {
			m := taskMainFixture(t) // real activation, Controller, inbox and Host
			events := []string{}
			responses := []protocol.JSONObject{}
			m.options.OnOutput = func(data []byte) error {
				frame := taskObject(t, data)
				if frame.String("type") == "control_response" {
					responses = append(responses, taskObject(t, frame.Get("response")))
				}
				return nil
			}
			closeFailure := errors.New("actual-end-session-raw-finally-error")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				raw, err := sdkhost.NewOwnerIterator(m.Owner, func(next *cooperative.Scope, it *sdkhost.OwnerIterator[string], _ sdkhost.IteratorRequest[string]) error {
					events = append(events, "raw:entry")
					return it.Yield(next, inboxControl("actual-end", map[string]any{"subtype": "end_session"}), func(done *cooperative.Scope, it *sdkhost.OwnerIterator[string], request sdkhost.IteratorRequest[string]) error {
						if !request.Returning {
							events = append(events, "raw:resumed")
							return errors.New("end_session resumed input rather than returning it")
						}
						events = append(events, "raw:finally")
						if mode == "finally-error" {
							return closeFailure
						}
						if mode == "finally-yield" {
							events = append(events, "raw:finally-yield")
							return it.Yield(done, "actual-finally-yield", func(last *cooperative.Scope, it *sdkhost.OwnerIterator[string], _ sdkhost.IteratorRequest[string]) error {
								events = append(events, "raw:finally-complete")
								return it.Complete(last)
							})
						}
						return it.CompleteReturn(done)
					})
				})
				if err != nil {
					return err
				}
				input, err := sdkhost.NewPlainStructuredInputWithDiagnostics(m.Owner, raw, m.diagnostics)
				if err != nil {
					return err
				}
				return m.Host.RunStream(scope, ctx, input, sdkhost.StreamOptions{})
			})
			if mode == "finally-error" {
				if err != closeFailure {
					t.Fatalf("end_session did not preserve actual close error identity: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			wantEvents := sourceEvents
			if mode == "finally-yield" {
				wantEvents = []string{"raw:entry", "raw:finally", "raw:finally-yield"}
			}
			if !reflect.DeepEqual(events, wantEvents) {
				t.Fatalf("actual end_session consumer skipped or overdrained raw close: %v want%v", events, wantEvents)
			}
			actual, _ := json.Marshal(responses)
			expected, _ := json.Marshal(sourceResponses)
			var a, b any
			_ = json.Unmarshal(actual, &a)
			_ = json.Unmarshal(expected, &b)
			if !reflect.DeepEqual(a, b) {
				t.Fatalf("ACK before close changed: Go%s Source%s", actual, expected)
			}
			if len(m.Controller.History()) != 0 {
				t.Fatal("end_session started model or consumed later user")
			}
		})
	}
}
