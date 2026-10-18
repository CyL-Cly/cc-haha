package mcptools

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpclient"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

type authenticationToolView struct{ config protocol.JSONObject }

// Source creates a fresh controller independent of the querying turn. Its
// listener/timer are unreferenced after returning the URL. Keep explicit jobs
// for process close, without retaining a whole ASK/native reference until the
// user visits that URL (which can take five minutes).
type authenticationToolJobs struct {
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	closed  bool
	workers sync.WaitGroup
}

func (jobs *authenticationToolJobs) start() (context.Context, func(), bool) {
	jobs.mu.Lock()
	defer jobs.mu.Unlock()
	if jobs.closed {
		return nil, nil, false
	}
	if jobs.ctx == nil {
		jobs.ctx, jobs.cancel = context.WithCancel(context.Background())
	}
	jobs.workers.Add(1)
	return jobs.ctx, jobs.workers.Done, true
}

func (jobs *authenticationToolJobs) close() {
	jobs.mu.Lock()
	jobs.closed = true
	if jobs.cancel != nil {
		jobs.cancel()
	}
	jobs.mu.Unlock()
	jobs.workers.Wait()
}

func authenticationToolDescription(name string, config protocol.JSONObject) string {
	transport := config.String("type")
	if transport == "" {
		transport = "stdio"
	}
	location := transport
	if address := config.String("url"); address != "" {
		location += " at " + address
	}
	return "The `" + name + "` MCP server (" + location + ") is installed but requires authentication. " +
		"Call this tool to start the OAuth flow — you'll receive an authorization URL to share with the user. " +
		"Once the user completes authorization in their browser, the server's real tools will become available automatically."
}

func (s *Service) publishAuthenticationTool(peer mcpruntime.Connection) {
	if s.authenticationTools == nil {
		s.authenticationTools = map[string]authenticationToolView{}
	}
	delete(s.authenticationTools, peer.Name)
	if peer.Status == "needs-auth" {
		s.authenticationTools[peer.Name] = authenticationToolView{config: peer.Config.Clone()}
	}
}

func (s *Service) hasAuthenticationTool(peer mcpruntime.Connection) bool {
	view, ok := s.authenticationTools[peer.Name]
	return ok && peer.Status == "needs-auth" && sameConnection(peer, mcpruntime.Connection{Name: peer.Name, Config: view.config})
}

func (s *Service) CloseAuthenticationTools() {
	if s != nil && s.authenticationJobs != nil && s.agentAuthentication == nil {
		s.authenticationJobs.close()
	}
}

func (s *Service) authenticationTool(peer mcpruntime.Connection) (toolschema.Definition, queryloop.RegisteredTool, error) {
	name := mcpclient.ToolName(peer.Name, "authenticate")
	description := authenticationToolDescription(peer.Name, peer.Config)
	definition := toolschema.Definition{Kind: "registered", Name: name, IsMCP: true, InputSchema: &toolschema.Schema{Kind: "object", UnknownKeys: "strip"}, Description: raw(description), Prompt: raw(description)}
	tool := queryloop.RegisteredTool{Name: name, IsMCP: true, PersistenceThreshold: 10000, ParseInput: func(input protocol.JSONObject) (protocol.JSONObject, error) {
		if !input.IsObject() {
			return protocol.JSONObject{}, errors.New("MCP authentication input must be an object")
		}
		return protocol.NewJSONObject(), nil
	}, IsConcurrencySafe: func(protocol.JSONObject) bool { return false }, MapResultJSON: func(data, id json.RawMessage) (protocol.JSONObject, error) {
		return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw("tool_result")}, protocol.JSONField{Name: "tool_use_id", Value: id}, protocol.JSONField{Name: "content", Value: object(data).Get("message")}), nil
	}}
	tool.StartCallJSON = func(ctx context.Context, input protocol.JSONObject, call queryloop.JSONToolCallContext) (*ownerasync.OwnerFuture[queryloop.ToolCallResult], error) {
		scope := s.options.ScopeForContext(call.Opaque)
		if scope == nil || !scope.BelongsTo(s.options.Owner) {
			return nil, cooperative.ErrRestricted
		}
		if ctx == nil {
			return nil, cooperative.ErrRestricted
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if s.agentResourceSnapshot && s.agentAuthentication == nil {
			return nil, cooperative.ErrRestricted
		}
		return s.startAuthenticationTool(scope, peer)
	}
	tool.CallJSON = func(ctx context.Context, input protocol.JSONObject, call queryloop.JSONToolCallContext) (queryloop.ToolCallResult, error) {
		future, err := tool.StartCallJSON(ctx, input, call)
		if err != nil {
			return queryloop.ToolCallResult{}, err
		}
		return future.Await(s.options.ScopeForContext(call.Opaque))
	}
	tool, err := queryloop.BindNativeMCP(tool, s.options.Runtime)
	return definition, tool, err
}

func (s *Service) startAuthenticationTool(scope *cooperative.Scope, peer mcpruntime.Connection) (*ownerasync.OwnerFuture[queryloop.ToolCallResult], error) {
	future, err := ownerasync.NewOwnerFuture[queryloop.ToolCallResult](s.options.Owner)
	if err != nil {
		return nil, err
	}
	config := peer.Config.Clone()
	transport := config.String("type")
	if transport == "" {
		transport = "stdio"
	}
	unsupported := ""
	if transport == "claudeai-proxy" {
		unsupported = "This is a claude.ai MCP connector. Ask the user to run /mcp and select \"" + peer.Name + "\" to authenticate."
	} else if transport != "http" && transport != "sse" {
		unsupported = "Server \"" + peer.Name + "\" uses " + transport + " transport which does not support OAuth from this tool. Ask the user to run /mcp and authenticate manually."
	}
	if unsupported != "" {
		err := future.Resolve(scope, queryloop.ToolCallResult{Data: raw(map[string]any{"status": "unsupported", "message": unsupported})})
		return future, err
	}
	ref, err := s.options.NativeBinding.Acquire(scope)
	if err != nil {
		return nil, err
	}
	process := s.authenticationProcess()
	life, finished, ok := process.authenticationJobs.start()
	if !ok {
		ref.DeliveryFinished()
		return nil, cooperative.ErrClosed
	}
	var once sync.Once
	deliver := func(value protocol.JSONObject) {
		once.Do(func() {
			receipt, err := s.options.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(next *cooperative.Scope) error {
				defer ref.DeliveryFinished()
				return future.Resolve(next, queryloop.ToolCallResult{Data: raw(value)})
			})
			if err != nil {
				_ = future.RejectAdministrative(err)
				ref.DeliveryFinished()
			} else {
				ref.ReclaimRejectedReceipt(receipt)
			}
		})
	}
	go func() {
		defer finished()
		failure := process.options.Runtime.AuthenticateWithOptions(life, peer.Name, config, func(address string) error {
			deliver(object(raw(map[string]any{"status": "auth_url", "authUrl": address, "message": "Ask the user to open this URL in their browser to authorize the " + peer.Name + " MCP server:\n\n" + address + "\n\nOnce they complete the flow, the server's tools will become available automatically."})))
			return nil
		}, mcpauth.FlowOptions{SkipBrowserOpen: true})
		if failure != nil {
			deliver(object(raw(map[string]any{"status": "error", "message": "Failed to start OAuth flow for " + peer.Name + ": " + failure.Error() + ". Ask the user to run /mcp and authenticate manually."})))
			if life.Err() == nil {
				process.logAuthenticationToolFailure(peer.Name, failure)
			}
			return
		}
		process.options.Runtime.AuthenticationToolAuthorized()
		deliver(object(raw(map[string]any{"status": "auth_url", "message": "Authentication completed silently for " + peer.Name + ". The server's tools should now be available."})))
		if life.Err() != nil {
			return
		}
		// The callback belongs to the call context, while the OAuth job belongs
		// to the process. A completed child must not cancel the browser flow.
		target := process
		receipt, err := target.options.Owner.SubmitIndependent(life, cooperative.Read, func(next *cooperative.Scope) error {
			count, err := target.reconnectAuthenticationTool(life, next, peer, func() bool { _, publish := s.authenticationAppStateTarget(); return publish })
			if err != nil {
				return err
			}
			if process.options.OnMCPDebug != nil {
				_ = process.options.OnMCPDebug(next, peer.Name, "OAuth complete, reconnected with "+strconv.Itoa(count)+" tool(s)")
			}
			return nil
		})
		if err == nil {
			err = receipt.Wait()
		}
		if err != nil && life.Err() == nil {
			process.logAuthenticationToolFailure(peer.Name, err)
		}
	}()
	return future, nil
}

func (s *Service) logAuthenticationToolFailure(name string, failure error) {
	if s.options.OnMCPError == nil {
		return
	}
	message := "OAuth flow failed after tool-triggered start: " + failure.Error()
	receipt, err := s.options.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(scope *cooperative.Scope) error {
		// Source logMCPError catches sink failures. Diagnostics never replace
		// an authorization result or the original failed callback.
		_ = s.options.OnMCPError(scope, name, message)
		return nil
	})
	if err == nil {
		_ = receipt.Wait()
	}
}
