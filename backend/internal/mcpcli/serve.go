package mcpcli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/mcpclient"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type ServeHost interface {
	Catalog(context.Context) ([]protocol.JSONObject, error)
	Call(context.Context, string, json.RawMessage) (protocol.JSONObject, error)
	Close(context.Context) error
}
type ServeOptions struct {
	Version          string
	ServerName       string          // Empty preserves the original claude/tengu CLI server.
	Capabilities     json.RawMessage // Empty preserves the original tools-only capability.
	Input            io.Reader
	Output           io.Writer
	Host             ServeHost
	OnTransportError func(error)
}

// Serve runs the actual newline-delimited JSON-RPC transport. Tool invocation
// errors are returned by Host as CallToolResult; lookup/protocol errors remain
// JSON-RPC errors. It does not create a model session or reuse stdout as logs.
func Serve(ctx context.Context, o ServeOptions) (failure error) {
	if ctx == nil || o.Input == nil || o.Output == nil || o.Host == nil || o.Version == "" {
		return errors.New("MCP serve requires actual transport, version and native tool context")
	}
	defer func() { failure = errors.Join(failure, o.Host.Close(context.Background())) }()
	reader := bufio.NewReader(o.Input)
	var requests sync.WaitGroup
	var write sync.Mutex
	var errMu sync.Mutex
	var sendFailure error
	type responseState struct{ cancelled bool }
	var activeMu sync.Mutex
	active := map[string]*responseState{}
	send := func(message protocol.JSONObject) {
		raw, err := message.MarshalJSON()
		if err == nil {
			raw, err = protocol.ParseAndStringifyJavaScriptJSON(raw)
		}
		if err == nil {
			write.Lock()
			_, err = o.Output.Write(append(raw, '\n'))
			write.Unlock()
		}
		if err != nil {
			errMu.Lock()
			if sendFailure == nil {
				sendFailure = err
			}
			errMu.Unlock()
		}
	}
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 && readErr == nil {
			request, err := decodeServeRequest(line)
			if err != nil {
				if o.OnTransportError != nil {
					o.OnTransportError(err)
				}
			} else if request.Get("id") == nil && request.String("method") == "notifications/cancelled" {
				params := object(request.Get("params"))
				id := params.Get("requestId")
				if len(id) > 0 && string(id) != "0" && string(id) != `""` {
					key, _ := protocol.ParseAndStringifyJavaScriptJSON(id)
					activeMu.Lock()
					if state := active[string(key)]; state != nil {
						state.cancelled = true
					}
					activeMu.Unlock()
				}
			} else if request.Get("id") != nil {
				key, _ := protocol.ParseAndStringifyJavaScriptJSON(request.Get("id"))
				state := &responseState{}
				activeMu.Lock()
				active[string(key)] = state
				activeMu.Unlock()
				requests.Add(1)
				go func(request protocol.JSONObject) {
					defer requests.Done()
					result, rpcFailure := serveRequest(ctx, o, request)
					response := protocol.NewJSONObject(protocol.JSONField{Name: "jsonrpc", Value: encode("2.0")}, protocol.JSONField{Name: "id", Value: request.Get("id")})
					if rpcFailure != nil {
						response.Set("error", encode(protocol.NewJSONObject(protocol.JSONField{Name: "code", Value: encode(rpcFailure.Code)}, protocol.JSONField{Name: "message", Value: encode(rpcFailure.Message)})))
					} else {
						response.Set("result", encode(result))
					}
					activeMu.Lock()
					cancelled := state.cancelled
					if active[string(key)] == state {
						delete(active, string(key))
					}
					activeMu.Unlock()
					if !cancelled {
						send(response)
					}
				}(request)
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				failure = readErr
			}
			break
		}
	}
	requests.Wait()
	errMu.Lock()
	failure = errors.Join(failure, sendFailure)
	errMu.Unlock()
	if err := context.Cause(ctx); err != nil {
		failure = errors.Join(failure, err)
	}
	return failure
}

type ServeRPCError struct {
	Code    int
	Message string
}

func (e *ServeRPCError) Error() string { return e.Message }
func decodeServeRequest(raw []byte) (protocol.JSONObject, error) {
	var request protocol.JSONObject
	if err := request.UnmarshalJSON(raw); err != nil {
		return request, err
	}
	if request.String("jsonrpc") != "2.0" || request.String("method") == "" {
		return request, errors.New("Invalid JSON-RPC message")
	}
	if id := request.Get("id"); id != nil {
		var value any
		if err := json.Unmarshal(id, &value); err != nil {
			return request, err
		}
		switch value.(type) {
		case string, float64:
		default:
			return request, errors.New("Invalid JSON-RPC request ID")
		}
	}
	return request, nil
}
func serveRequest(ctx context.Context, o ServeOptions, r protocol.JSONObject) (protocol.JSONObject, *ServeRPCError) {
	params := object(r.Get("params"))
	if failure := serveParamFailure(r.String("method"), r.Get("params")); failure != nil {
		return protocol.JSONObject{}, failure
	}
	switch r.String("method") {
	case "initialize":
		version := params.String("protocolVersion")
		supported := map[string]bool{"2025-11-25": true, "2025-06-18": true, "2025-03-26": true, "2024-11-05": true, "2024-10-07": true}
		if !supported[version] {
			version = mcpclient.LatestProtocolVersion
		}
		name, capabilities := o.ServerName, o.Capabilities
		if name == "" {
			name = "claude/tengu"
		}
		if len(capabilities) == 0 {
			capabilities = json.RawMessage(`{"tools":{}}`)
		}
		return protocol.NewJSONObject(protocol.JSONField{Name: "protocolVersion", Value: encode(version)}, protocol.JSONField{Name: "capabilities", Value: capabilities}, protocol.JSONField{Name: "serverInfo", Value: encode(protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: encode(name)}, protocol.JSONField{Name: "version", Value: encode(o.Version)}))}), nil
	case "ping":
		return protocol.NewJSONObject(), nil
	case "tools/list":
		tools, err := o.Host.Catalog(ctx)
		if err != nil {
			return protocol.JSONObject{}, &ServeRPCError{-32603, err.Error()}
		}
		return protocol.NewJSONObject(protocol.JSONField{Name: "tools", Value: encode(tools)}), nil
	case "tools/call":
		result, err := o.Host.Call(ctx, params.String("name"), params.Get("arguments"))
		if err != nil {
			return protocol.JSONObject{}, &ServeRPCError{-32603, err.Error()}
		}
		return result, nil
	default:
		return protocol.JSONObject{}, &ServeRPCError{-32601, "Method not found"}
	}
}
