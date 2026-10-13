// Package mcpclient implements the native MCP JSON-RPC client and the public
// stdio, Streamable HTTP and legacy SSE transports used by the original client.
// Configuration, authentication storage and Harness registration belong to the
// caller; this package never reads user settings or launches a legacy runtime.
package mcpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

const LatestProtocolVersion = "2025-11-25"

var supportedProtocolVersions = map[string]bool{LatestProtocolVersion: true, "2025-06-18": true, "2025-03-26": true, "2024-11-05": true, "2024-10-07": true}

type Config struct {
	AuthToken string
	Type      string
	Command   string
	Args      []string
	Env       map[string]string
	URL       string
	Headers   map[string]string
}

// AuthProvider binds an already resolved account to the transport. A nil
// provider sends only configured headers. 401 remains an actionable AuthError;
// no login, credential lookup or fake authentication is performed here.
type AuthProvider interface {
	Token(context.Context) (string, error)
	Authorize(context.Context, AuthChallenge) error
}
type AuthChallenge struct {
	URL             string
	Status          int
	WWWAuthenticate string
}
type AuthError struct{ Challenge AuthChallenge }

func (e *AuthError) Error() string { return "Unauthorized" }

type Options struct {
	SDK          *SDKControlBindings
	Name         string
	ClientInfo   protocol.JSONObject
	Capabilities protocol.JSONObject
	Version      string
	UserAgent    string
	// Environment is the resolved subprocess environment, including the caller's
	// shell PATH. It is explicit, so tests and host integrations do not implicitly
	// inherit real credentials. Config.Env overlays this environment.
	Environment          map[string]string
	CWD                  string
	RootURI              string
	HTTPClient           *http.Client
	Auth                 AuthProvider
	ConnectTimeout       time.Duration
	RequestTimeout       time.Duration
	ToolTimeout          time.Duration
	HTTPTimeout          time.Duration
	ReconnectDelay       time.Duration
	MaxReconnectAttempts int
	OnNotification       func(string, protocol.JSONObject)
	OnError              func(error)
	OnClose              func(intentional bool)
	// SessionIngressToken is the same once-read token producer used by RemoteIO.
	// Configured server headers take precedence; ws-ide uses only its lock token.
	SessionIngressToken func() string
	// OnRequest may replace roots/list and elicitation/create or implement other
	// explicitly registered capabilities. Default handlers are ping, one root,
	// and elicitation cancellation, matching the original initialization window.
	OnRequest func(context.Context, string, protocol.JSONObject) (protocol.JSONObject, error)
}

type RequestOptions struct {
	Timeout    time.Duration
	OnProgress func(protocol.JSONObject)
	// CancelReason is the originating SDK/host abort reason. Without an explicit
	// reason, context cancellation uses the original AbortSignal default text.
	CancelReason string
}

type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("MCP error %d: %s", e.Code, e.Message) }

func (e *RPCError) UnmarshalJSON(b []byte) error {
	o := nested(b)
	var code json.Number
	if !o.IsObject() || !hasType(o.Get("code"), "number") || json.Unmarshal(o.Get("code"), &code) != nil || !integerNumber(code) || !hasType(o.Get("message"), "string") {
		return errors.New("Invalid JSON-RPC error")
	}
	n, _ := code.Float64()
	e.Code = int(n)
	e.Message = o.String("message")
	e.Data = o.Get("data")
	return nil
}

type HTTPError struct {
	Status  int
	Message string
}

func (e *HTTPError) Error() string { return "Streamable HTTP error: " + e.Message }

type TransportError struct{ Message string }

func (e *TransportError) Error() string { return e.Message }

type rpcMessage struct {
	JSONRPC   string              `json:"jsonrpc"`
	ID        json.RawMessage     `json:"id,omitempty"`
	Method    string              `json:"method,omitempty"`
	Params    protocol.JSONObject `json:"params,omitzero"`
	Result    json.RawMessage     `json:"result,omitempty"`
	Error     *RPCError           `json:"error,omitempty"`
	hasMethod bool
}

func (m rpcMessage) MarshalJSON() ([]byte, error) {
	o := object()
	if m.hasMethod || m.Method != "" {
		o.Set("method", raw(m.Method))
		if m.Params.IsObject() {
			o.Set("params", raw(m.Params))
		}
	}
	o.Set("jsonrpc", raw(m.JSONRPC))
	if len(m.ID) > 0 {
		o.Set("id", m.ID)
	}
	if len(m.Result) > 0 {
		o.Set("result", m.Result)
	}
	if m.Error != nil {
		o.Set("error", raw(m.Error))
	}
	return o.MarshalJSON()
}

type transport interface {
	start(context.Context, func(rpcMessage), func(error), func()) error
	send(context.Context, rpcMessage) error
	setProtocolVersion(string)
	close() error
}

func object(fields ...protocol.JSONField) protocol.JSONObject {
	return protocol.NewJSONObject(fields...)
}
func field(name string, value any) protocol.JSONField {
	return protocol.JSONField{Name: name, Value: raw(value)}
}
func raw(value any) json.RawMessage {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if encoder.Encode(value) != nil {
		return nil
	}
	b := bytes.TrimSpace(buffer.Bytes())
	return protocol.StringifyValidatedJavaScriptJSON(b)
}

func normalizeOptions(o Options) Options {
	if o.Version == "" {
		o.Version = "unknown"
	}
	if !o.ClientInfo.IsObject() {
		o.ClientInfo = object(field("name", "claude-code"), field("title", "Claude Code"), field("version", o.Version), field("description", "Anthropic's agentic coding tool"), field("websiteUrl", "https://claude.com/claude-code"))
	}
	if !o.Capabilities.IsObject() {
		o.Capabilities = object(field("roots", object()), field("elicitation", object()))
	}
	if o.UserAgent == "" {
		parts := []string{"cli"}
		if sdk := o.Environment["CLAUDE_AGENT_SDK_VERSION"]; sdk != "" {
			parts = append(parts, "agent-sdk/"+sdk)
		}
		if app := o.Environment["CLAUDE_AGENT_SDK_CLIENT_APP"]; app != "" {
			parts = append(parts, "client-app/"+app)
		}
		o.UserAgent = "claude-code/" + provider.SourceCompatibilityVersion + " (" + strings.Join(parts, ", ") + ")"
	}
	if o.RootURI == "" && o.CWD != "" {
		o.RootURI = "file://" + o.CWD
	}
	if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{}
	}
	if o.ConnectTimeout <= 0 {
		o.ConnectTimeout = 30 * time.Second
	}
	if o.RequestTimeout <= 0 {
		o.RequestTimeout = 60 * time.Second
	}
	if o.ToolTimeout <= 0 {
		o.ToolTimeout = 100_000_000 * time.Millisecond
	}
	if o.HTTPTimeout <= 0 {
		o.HTTPTimeout = 60 * time.Second
	}
	if o.ReconnectDelay <= 0 {
		o.ReconnectDelay = time.Second
	}
	if o.MaxReconnectAttempts <= 0 {
		o.MaxReconnectAttempts = 2
	}
	return o
}
