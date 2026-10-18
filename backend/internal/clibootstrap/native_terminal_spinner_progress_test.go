package clibootstrap

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualNativeInteractiveSpinnerSeesRequestStartAndUTF16WithoutChangingSDKOutput(t *testing.T) {
	var requests atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		body, _ := io.ReadAll(request.Body)
		if requests.Add(1) == 2 {
			if !bytes.Contains(body, []byte("Task not found")) {
				t.Error("Original actual TaskUpdate result missing", string(body))
			}
			emitPlanFixture(w, 2, "", "SPINNER_STAGE_DONE")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []string{
			`{"type":"message_start","message":{"id":"spinner-actual-source","type":"message","role":"assistant","model":"fixture-model","content":[],"usage":{"input_tokens":1,"output_tokens":0}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"A😀\ud800"}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"SIGNATURE_DOES_NOT_COUNT"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"spinner-task","name":"TaskUpdate","input":{}}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"taskId\":\"99\",\"status\":\"in_progress\"}"}}`,
			`{"type":"content_block_stop","index":1}`,
			`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":1}}`,
			`{"type":"message_stop"}`,
		} {
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", mainObj([]byte(event)).String("type"), event)
		}
	}))
	defer peer.Close()
	var observations []tui.TerminalSpinnerProgress
	var publicOutput bytes.Buffer
	genericRequests := 0
	m := backgroundAgentMainWithOptions(t, peer.URL, nil, func(options *MainPrintOptions) {
		options.Interactive = true
		options.Arguments.Tools = []string{"TaskUpdate"}
		options.OnTerminalSpinner = func(value tui.TerminalSpinnerProgress) error { observations = append(observations, value); return nil }
		options.OnQueryYield = func(_ *cooperative.Scope, value *queryloop.Yield) error {
			if value.RequestStart {
				genericRequests++
			}
			return nil
		}
		options.OnOutput = func(data []byte) error { _, err := publicOutput.Write(data); return err }
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := m.RunTurn(ctx, mainObj([]byte(`{"type":"user","message":{"role":"user","content":"Actual original spinner stream"}}`))); err != nil {
		t.Fatal(err)
	}
	count, length := 0, 0
	seen := map[string]bool{}
	for _, value := range observations {
		if value.Mode == "requesting" {
			count++
		}
		seen[value.Mode] = true
		length += value.AddedResponseUnits
	}
	wantLength := 4 + len(`{"taskId":"99","status":"in_progress"}`) + len("SPINNER_STAGE_DONE")
	if count != 2 || length != wantLength || !seen["thinking"] || !seen["tool-input"] || !seen["tool-use"] || genericRequests != 0 || requests.Load() != 2 {
		t.Fatalf("Actual interactive spinner callback missing: requesting%d units%d/%d modes%v generic%d HTTP%d", count, length, wantLength, seen, genericRequests, requests.Load())
	}
	if bytes.Contains(publicOutput.Bytes(), []byte("terminal_spinner")) || bytes.Contains(publicOutput.Bytes(), []byte("stream_request_start")) {
		t.Fatal("Internal spinner observation changed the public SDK protocol", publicOutput.String())
	}
}
