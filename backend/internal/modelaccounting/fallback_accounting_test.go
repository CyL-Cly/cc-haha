package modelaccounting

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestFallbackFinallyChargesBothRequestsAndPreservesCompleteUsage(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	session, _ := NewOwnedSession(owner, nil)
	binding, _ := NewMainContext(owner)
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		attempt, err := session.BeginRequest(scope, originalAccountingInput("fixture-model", "sdk"), binding)
		if err != nil {
			return err
		}
		if err = observeOriginalDelta(scope, attempt); err != nil {
			return err
		}
		if err = attempt.ObserveFallback(scope, []byte(`{"usage":{"input_tokens":8,"output_tokens":3,"service_tier":"priority","inference_geo":"us","speed":"fast","server_tool_use":{"web_search_requests":2},"cache_creation":{"ephemeral_1h_input_tokens":4},"iterations":[{"type":"message","input_tokens":8}]}}`)); err != nil {
			return err
		}
		// The consumer closes at the assistant yield: normal-chain continuation
		// does not run, but finally must charge the independent fallback once.
		id := "not-reached"
		if err = attempt.Finish(scope, false, &id); err != nil {
			return err
		}
		first, _ := session.total.MarshalJSON()
		if err = attempt.Finish(scope, false, &id); err != nil {
			return err
		}
		second, _ := session.total.MarshalJSON()
		if !bytes.Equal(first, second) {
			return errors.New("fallback charged twice")
		}
		for name, want := range map[string]float64{"input_tokens": 12, "output_tokens": 6, "cache_read_input_tokens": 2, "cache_creation_input_tokens": 1} {
			if num(session.total.Get(name)) != want {
				t.Fatalf("%s got%s want%v", name, session.total.Get(name), want)
			}
		}
		if session.total.String("service_tier") != "priority" || session.total.String("inference_geo") != "us" || session.total.String("speed") != "fast" || string(session.total.Get("iterations")) != `[{"type":"message","input_tokens":8}]` || session.lastRequestID != nil {
			return errors.New("fallback usage metadata or return-chain policy lost")
		}
		var tools protocol.JSONObject
		_ = tools.UnmarshalJSON(session.total.Get("server_tool_use"))
		if num(tools.Get("web_search_requests")) != 2 || num(tools.Get("web_fetch_requests")) != 0 {
			return errors.New("partial nested fallback usage not normalized")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
