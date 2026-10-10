package sdkhost

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"testing"
)

func TestStopTaskSDKControlPreservesSuccessGoneAndErrorEnvelopes(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	for _, row := range []struct {
		result, errorJSON, expected string
		failure                     error
	}{{`{"ok":true,"alreadyGone":false}`, "", `{"type":"control_response","response":{"subtype":"success","request_id":"original-request","response":{}}}`, nil}, {`{"ok":true,"alreadyGone":true}`, "", `{"type":"control_response","response":{"subtype":"success","request_id":"original-request","response":{"stopped":false,"reason":"not_found"}}}`, nil}, {`{"ok":false,"message":"not running"}`, "", `{"type":"control_response","response":{"subtype":"error","request_id":"original-request","error":"not running"}}`, nil}, {"", "", `{"type":"control_response","response":{"subtype":"error","request_id":"original-request","error":"native failure"}}`, errors.New("native failure")}} {
		var emitted []byte
		calls := 0
		h := &Host{options: Options{Owner: owner, OnPhase: func(string) {}, StopTask: func(scope *cooperative.Scope, id string) (protocol.JSONObject, error) {
			if !scope.BelongsTo(owner) || id != "actual-task" {
				t.Fatal("foreign owner or substituted id")
			}
			calls++
			return object([]byte(row.result)), row.failure
		}, Output: Output{Enqueue: func(data []byte) error { emitted = append(emitted, data...); return nil }}}}
		e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			return h.stopTaskControl(scope, object([]byte(`{"type":"control_request","request_id":"original-request","request":{"subtype":"stop_task","task_id":"actual-task"}}`)))
		})
		if e != nil || calls != 1 || string(protocol.StringifyValidatedJavaScriptJSON(emitted)) != row.expected {
			t.Errorf("control Go%s %v count%d Source%s", emitted, e, calls, row.expected)
		}
	}
}
