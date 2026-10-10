package sdkhost

import (
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// NativeSDKMCPState is the one owner's mutable sdkMcpConfigs and live control
// transport. The MCP runtime borrows it; it is not another connection/catalog.
type NativeSDKMCPState struct {
	owner          *cooperative.Owner
	configurations protocol.JSONObject
	pending        map[string]*OwnerFuture[json.RawMessage]
	enqueue        func(*cooperative.Scope, protocol.JSONObject) error
	uuid           func() string
	open           bool
}

func NewNativeSDKMCPState(owner *cooperative.Owner) (*NativeSDKMCPState, error) {
	if owner == nil {
		return nil, cooperative.ErrRestricted
	}
	return &NativeSDKMCPState{owner: owner, configurations: protocol.NewJSONObject(), pending: map[string]*OwnerFuture[json.RawMessage]{}}, nil
}
func (s *NativeSDKMCPState) Configurations(scope *cooperative.Scope) (protocol.JSONObject, error) {
	if s == nil || !scope.BelongsTo(s.owner) {
		return protocol.JSONObject{}, cooperative.ErrRestricted
	}
	return s.configurations.Clone(), nil
}
func (s *NativeSDKMCPState) bind(scope *cooperative.Scope, enqueue func(*cooperative.Scope, protocol.JSONObject) error, uuid func() string) error {
	if s == nil || !scope.BelongsTo(s.owner) || enqueue == nil || uuid == nil {
		return cooperative.ErrRestricted
	}
	s.enqueue = enqueue
	s.uuid = uuid
	s.open = true
	return nil
}

// Source's initial object assignment adds names without removing prior names.
func (s *NativeSDKMCPState) RegisterConfigurations(scope *cooperative.Scope, names []string) error {
	return s.RegisterInitializeConfigurations(scope, raw(names))
}
func (s *NativeSDKMCPState) Send(scope *cooperative.Scope, name string, message protocol.JSONObject) (*OwnerFuture[json.RawMessage], error) {
	if s == nil || !scope.BelongsTo(s.owner) {
		return nil, cooperative.ErrRestricted
	}
	if !s.open || s.enqueue == nil || s.uuid == nil {
		return nil, errors.New("Stream closed")
	}
	id := s.uuid()
	future, e := NewOwnerFuture[json.RawMessage](s.owner)
	if e != nil {
		return nil, e
	}
	s.pending[id] = future
	request := protocol.NewJSONObject(protocol.JSONField{Name: "subtype", Value: quote("mcp_message")}, protocol.JSONField{Name: "server_name", Value: quote(name)}, protocol.JSONField{Name: "message", Value: raw(message)})
	event := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("control_request")}, protocol.JSONField{Name: "request_id", Value: quote(id)}, protocol.JSONField{Name: "request", Value: raw(request)})
	if e = s.enqueue(scope, event); e != nil {
		delete(s.pending, id)
		_ = future.Reject(scope, e)
		return nil, e
	}
	return future, nil
}
func (h *Host) SendNativeSDKMCPMessage(scope *cooperative.Scope, name string, message protocol.JSONObject) (*OwnerFuture[json.RawMessage], error) {
	if h == nil || !scope.BelongsTo(h.options.Owner) || h.options.NativeSDKMCP == nil {
		return nil, cooperative.ErrRestricted
	}
	return h.options.NativeSDKMCP.Send(scope, name, message)
}
func (h *Host) NativeSDKMCPConfigurations(scope *cooperative.Scope) (protocol.JSONObject, error) {
	if h == nil || !scope.BelongsTo(h.options.Owner) || h.options.NativeSDKMCP == nil {
		return protocol.JSONObject{}, cooperative.ErrRestricted
	}
	return h.options.NativeSDKMCP.Configurations(scope)
}
func (s *NativeSDKMCPState) response(scope *cooperative.Scope, message protocol.JSONObject) (bool, error) {
	if s == nil || !scope.BelongsTo(s.owner) {
		return false, cooperative.ErrRestricted
	}
	response := object(message.Get("response"))
	id := response.String("request_id")
	future := s.pending[id]
	if future == nil {
		return false, nil
	}
	delete(s.pending, id)
	if response.String("subtype") == "error" {
		return true, future.Reject(scope, errors.New(response.String("error")))
	}
	payload := object(response.Get("response"))
	value := payload.Get("mcp_response")
	// z.any preserves undefined, null and all JSON shapes here. Parsing belongs
	// to the actual MCP Client callback, not to the outer control protocol.
	return true, future.Resolve(scope, append(json.RawMessage(nil), value...))
}
func (s *NativeSDKMCPState) close(scope *cooperative.Scope) error {
	if s == nil || !scope.BelongsTo(s.owner) {
		return cooperative.ErrRestricted
	}
	s.open = false
	for id, future := range s.pending {
		delete(s.pending, id)
		if e := future.Reject(scope, errors.New("Tool permission stream closed before response received")); e != nil {
			return e
		}
	}
	return nil
}
