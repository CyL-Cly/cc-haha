package requestretry

import (
	"context"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestOriginalNativeUserPromptWarningReachesExistingPrepareAndPhysicalRequest(t *testing.T) {
	server := startFixtureServer(t, "success", func(int) (int, string) { return 200, responseWire })
	input := standardInput()
	input.Context.Host.Profile = requestcontext.NativeDirectRelayProfile
	input.Context.Host.Env["ANTHROPIC_BASE_URL"] = server.origin
	record := originalHistoryRecord(t, `{"type":"system","subtype":"informational","content":"UserPromptSubmit operation blocked by hook:\nreason \ud800\n\nOriginal prompt: ERASED_ORIGINAL","isMeta":false,"timestamp":"2023-11-14T22:13:20.000Z","uuid":"actual-warning","level":"warning"}`)
	before, _ := record.MarshalJSON()
	input.Context.History = append([]protocol.JSONObject{record}, input.Context.History...)
	stream, err := Open(context.Background(), input, standardDeps())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	body := <-server.bodies
	if strings.Contains(body, "ERASED_ORIGINAL") || strings.Contains(body, "UserPromptSubmit") || !strings.Contains(body, "original request") {
		t.Fatal("original warning entered or replaced actual API user", body)
	}
	after, _ := record.MarshalJSON()
	if string(before) != string(after) {
		t.Fatal("admission mutated original warning/history")
	}
}

func TestOriginalUserPromptWarningAdmissionStillRejectsUnproducedOrMalformedEnvelopes(t *testing.T) {
	for _, mutate := range []func(*protocol.JSONObject){func(r *protocol.JSONObject) { r.Set("content", []byte(`"other informational producer"`)) }, func(r *protocol.JSONObject) { r.Set("level", []byte(`"info"`)) }, func(r *protocol.JSONObject) { r.Delete("uuid") }, func(r *protocol.JSONObject) { r.Set("isMeta", []byte("true")) }, func(r *protocol.JSONObject) { r.Set("isVirtual", []byte("false")) }, func(r *protocol.JSONObject) { r.Set("content", []byte("null")) }} {
		input := standardInput()
		input.Context.Host.Profile = requestcontext.NativeDirectRelayProfile
		record := originalHistoryRecord(t, `{"type":"system","subtype":"informational","content":"UserPromptSubmit operation blocked by hook:\nreason\n\nOriginal prompt: ERASED_ORIGINAL","isMeta":false,"timestamp":"2023-11-14T22:13:20.000Z","uuid":"actual-warning","level":"warning"}`)
		mutate(&record)
		input.Context.History = append([]protocol.JSONObject{record}, input.Context.History...)
		if err := validated(input, standardDeps()); err == nil {
			t.Fatal("unproduced/malformed warning bypassed original guard")
		}
	}
}
