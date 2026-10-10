package apipresentation

import (
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"strings"
	"testing"
	"time"
)

func TestStreamErrorOriginalIdentityAndDetail(t *testing.T) {
	factory, _ := messages.NewFactory(func() string { return "original" }, func() time.Time { return time.UnixMilli(1700000000000) })
	domain := Domain{direct: true}
	raw := json.RawMessage(`"original \ud800 tail"`)
	original, e := NewOriginalError(raw)
	if e != nil {
		t.Fatal(e)
	}
	raw[1] = 'X'
	envelope, e := StreamFailure(original, domain, factory)
	if e != nil {
		t.Fatal(e)
	}
	wire, _ := envelope.MarshalJSON()
	if !strings.Contains(string(wire), `\ud800`) {
		t.Fatalf("raw UTF16 lost %s", wire)
	}
	for _, row := range []struct{ payload, message, want string }{
		{`{"error":{"message":"original message \ud800   "}}`, `raw`, `"API Error: original message \ud800"`},
		{`{"error":{"error":{"message":"late detail"}}}`, `raw`, `"API Error: late detail"`},
		{`"plain detail "`, `raw`, `"API Error: plain detail"`},
		{`{"error":{}}`, `raw`, `"API Error: Request failed"`},
		{`null`, `"nested source"`, `"API Error: nested source"`},
		{`null`, `plain raw   `, `"API Error: plain raw"`},
		{`{"message":" <HTML>original document"}`, `raw`, `"API Error: Received an HTML error response from the server."`},
	} {
		sdk := &provider.SDKStreamError{Class: "APIError", Kind: "sse_api", Payload: []byte(row.payload), Message: row.message}
		env, e := StreamFailure(sdk, domain, factory)
		if e != nil {
			t.Fatal(e)
		}
		var content []protocol.JSONObject
		message, _ := env.MessageJSON()
		var m protocol.JSONObject
		_ = m.UnmarshalJSON(message)
		_ = json.Unmarshal(m.Get("content"), &content)
		if string(content[0].Get("text")) != row.want {
			t.Errorf("%s => %s want%s", row.payload, content[0].Get("text"), row.want)
		}
	}
	if _, e = NewOriginalError([]byte(`null`)); e == nil {
		t.Fatal("nonstring Error message")
	}
	for _, text := range []string{"maximum context length", "image not supported", "image_url text expected", "Your credit balance is too low", "The PDF specified was not valid", "x-api-key invalid", "invalid model name"} {
		o, _ := NewOriginalError(quote(text))
		if _, e = StreamFailure(o, domain, factory); e == nil {
			t.Fatal("specialized precedence admitted", text)
		}
	}
	for _, failure := range []error{errors.New("unrepresented native error"), (*OriginalError)(nil), &provider.SDKStreamError{Kind: "syntax", Class: "SyntaxError"}, &provider.SDKStreamError{Kind: "sse_api", Class: "APIError", Status: new(int)}} {
		if _, e = StreamFailure(failure, domain, factory); e == nil {
			t.Fatal("native class/status admitted")
		}
	}
	if _, e = StreamFailure(original, Domain{}, factory); e == nil {
		t.Fatal("domain missing")
	}
	if _, e = StreamFailure(original, domain, nil); e == nil {
		t.Fatal("factory missing")
	}
}
