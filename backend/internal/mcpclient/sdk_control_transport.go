package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// SDKControlBindings borrows the actual StructuredIO control channel. It is
// supplied by the owning Host bridge and never performs HTTP/stdin itself.
type SDKControlBindings struct {
	Send func(context.Context, protocol.JSONObject) (json.RawMessage, error)
}
type sdkControlTransport struct {
	mu       sync.Mutex
	bindings *SDKControlBindings
	closed   bool
	receive  func(rpcMessage)
	onError  func(error)
	onClose  func()
}

func (t *sdkControlTransport) start(_ context.Context, receive func(rpcMessage), onError func(error), onClose func()) error {
	if t.bindings == nil || t.bindings.Send == nil {
		return errors.New("SDK MCP transport requires actual control-message binding")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.receive = receive
	t.onError = onError
	t.onClose = onClose
	return nil
}
func (t *sdkControlTransport) send(_ context.Context, message rpcMessage) error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return errors.New("Transport is closed")
	}
	receive, onError, send := t.receive, t.onError, t.bindings.Send
	t.mu.Unlock()
	// The existing Client.Request already starts transport.send independently.
	// This send promise has no model AbortSignal; a canceled request may settle
	// locally while the separate control response remains pending.
	data, e := json.Marshal(message)
	if e != nil {
		return e
	}
	var packet protocol.JSONObject
	if e = packet.UnmarshalJSON(data); e != nil {
		return e
	}
	result, e := send(context.Background(), packet)
	if e != nil {
		return e
	}
	data = result
	var response rpcMessage
	if e = json.Unmarshal(data, &response); e != nil {
		onError(e)
		return nil
	}
	receive(response)
	return nil
}
func (t *sdkControlTransport) close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	closed := t.onClose
	t.mu.Unlock()
	if closed != nil {
		closed()
	}
	return nil
}
func (*sdkControlTransport) setProtocolVersion(string) {}

// DeliverSDKMessage is the inbound SDK notification boundary. The caller must
// already hold an actual connected SDK client from the shared runtime snapshot.
func (c *Client) DeliverSDKMessage(message protocol.JSONObject) error {
	t, ok := c.transport.(*sdkControlTransport)
	if !ok {
		return errors.New("MCP client is not an SDK control transport")
	}
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	var packet rpcMessage
	if err = json.Unmarshal(data, &packet); err != nil {
		return err
	}
	t.mu.Lock()
	receive := t.receive
	t.mu.Unlock()
	if receive != nil {
		receive(packet)
	}
	return nil
}
