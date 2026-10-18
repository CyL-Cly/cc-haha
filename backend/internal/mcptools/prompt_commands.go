package mcptools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpclient"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
)

type promptCatalogue struct {
	peer        mcpruntime.Connection
	rows        []protocol.JSONObject
	unsubscribe func()
}

type pendingPrompts struct {
	peer mcpruntime.Connection
	read *ownerasync.OwnerFuture[[]protocol.JSONObject]
}

func (s *Service) beginPromptCommands(scope *cooperative.Scope, peers []mcpruntime.Connection) ([]pendingPrompts, error) {
	reads := []pendingPrompts{}
	for _, peer := range peers {
		// Source setupSdkMcpClients returns clients/tools only. Ordinary MCP
		// prompt discovery must not invent an SDK prompt-command consumer.
		if peer.Status != "connected" || peer.Client == nil || peer.Config.String("type") == "sdk" {
			continue
		}
		if previous, found := s.promptCommands[peer.Name]; found && previous.peer.Client == peer.Client {
			continue
		}
		read, err := native(s, scope, func() ([]protocol.JSONObject, error) {
			return s.options.Runtime.FetchPrompts(context.Background(), peer)
		})
		if err != nil {
			return nil, err
		}
		reads = append(reads, pendingPrompts{peer, read})
	}
	return reads, nil
}

func (s *Service) finishPromptCommands(scope *cooperative.Scope, reads []pendingPrompts) error {
	for _, item := range reads {
		rows, err := item.read.Await(scope)
		if err != nil {
			return err
		}
		if err := s.installPromptCommands(scope, item.peer, rows); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) installPromptCommands(scope *cooperative.Scope, peer mcpruntime.Connection, rows []protocol.JSONObject) error {
	if !scope.BelongsTo(s.options.Owner) {
		return cooperative.ErrRestricted
	}
	if s.promptCommands == nil {
		s.promptCommands = map[string]promptCatalogue{}
	}
	previous := s.promptCommands[peer.Name]
	if previous.peer.Client != peer.Client && previous.unsubscribe != nil {
		previous.unsubscribe()
	}
	current := promptCatalogue{peer: peer, rows: cloneResourceRows(rows)}
	if previous.peer.Client == peer.Client {
		current.unsubscribe = previous.unsubscribe
	}
	prompts, _ := peer.Client.Capabilities().Object("prompts")
	if string(prompts.Get("listChanged")) == "true" && current.unsubscribe == nil && s.options.OnCatalogChanged != nil {
		unsubscribe, err := peer.Client.SubscribeNotifications(func(method string, _ protocol.JSONObject) {
			if method != "notifications/prompts/list_changed" {
				return
			}
			_, _ = s.options.Owner.SubmitIndependent(context.Background(), cooperative.Input, func(next *cooperative.Scope) error {
				old, found := s.promptCommands[peer.Name]
				if !found || old.peer.Client != peer.Client {
					return nil
				}
				if err := s.options.Runtime.InvalidatePrompts(peer); err != nil {
					return err
				}
				read, err := native(s, next, func() ([]protocol.JSONObject, error) {
					return s.options.Runtime.FetchPrompts(context.Background(), peer)
				})
				if err != nil {
					return err
				}
				rows, err := read.Await(next)
				if err != nil {
					return err
				}
				if live := s.promptCommands[peer.Name]; live.peer.Client != peer.Client {
					return nil
				}
				old.rows = cloneResourceRows(rows)
				s.promptCommands[peer.Name] = old
				if s.options.OnCatalogChanged != nil {
					return s.options.OnCatalogChanged(next)
				}
				return nil
			})
		})
		if err != nil {
			return err
		}
		current.unsubscribe = unsubscribe
	}
	s.promptCommands[peer.Name] = current
	return nil
}

func (s *Service) PromptCommands(scope *cooperative.Scope) ([]skillcommands.Definition, error) {
	if !scope.BelongsTo(s.options.Owner) {
		return nil, cooperative.ErrRestricted
	}
	definitions := []skillcommands.Definition{}
	for _, peer := range s.connections {
		catalogue, found := s.promptCommands[peer.Name]
		if !found || peer.Status != "connected" || catalogue.peer.Client != peer.Client {
			continue
		}
		for _, row := range catalogue.rows {
			name := "mcp__" + mcpclient.NormalizeName(peer.Name) + "__" + row.String("name")
			var arguments []protocol.JSONObject
			_ = json.Unmarshal(row.Get("arguments"), &arguments)
			argumentNames := []string{}
			for _, argument := range arguments {
				argumentNames = append(argumentNames, argument.String("name"))
			}
			metadata := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw("prompt")}, protocol.JSONField{Name: "name", Value: raw(name)}, protocol.JSONField{Name: "description", Value: row.Get("description")}, protocol.JSONField{Name: "hasUserSpecifiedDescription", Value: raw(row.String("description") != "")}, protocol.JSONField{Name: "contentLength", Value: raw(0)}, protocol.JSONField{Name: "isHidden", Value: raw(false)}, protocol.JSONField{Name: "isMcp", Value: raw(true)}, protocol.JSONField{Name: "progressMessage", Value: raw("running")}, protocol.JSONField{Name: "userFacingName", Value: raw(peer.Name + ":" + row.String("name") + " (MCP)")}, protocol.JSONField{Name: "argNames", Value: raw(argumentNames)}, protocol.JSONField{Name: "source", Value: raw("mcp")})
			if row.Get("description") == nil {
				metadata.Set("description", raw(""))
			}
			definition, err := skillcommands.NewNativeContentCommand(metadata, func(ctx context.Context, args *string, invocation skillcommands.Invocation) ([]protocol.JSONObject, error) {
				return s.invokePromptCommand(ctx, invocation.NativeScope, peer, row.String("name"), argumentNames, args)
			})
			if err != nil {
				return nil, err
			}
			definitions = append(definitions, definition)
		}
	}
	return definitions, nil
}

func (s *Service) invokePromptCommand(ctx context.Context, scope *cooperative.Scope, peer mcpruntime.Connection, name string, names []string, args *string) ([]protocol.JSONObject, error) {
	if !scope.BelongsTo(s.options.Owner) || s.options.ResultOptions == nil {
		return nil, cooperative.ErrRestricted
	}
	text := ""
	if args != nil {
		text = *args
	}
	values := strings.Split(text, " ")
	arguments := protocol.NewJSONObject()
	for index, name := range names {
		if index < len(values) {
			arguments.Set(name, raw(values[index]))
		}
	}
	options, err := s.options.ResultOptions(scope)
	if err != nil {
		return nil, err
	}
	// Rich media siblings retain the real native reference until all physical
	// image/blob work settles, even if cancellation rejects the prompt first.
	ref, err := s.options.NativeBinding.Acquire(scope)
	if err != nil {
		return nil, err
	}
	options.OnSettled = ref.DeliveryFinished
	read, err := native(s, scope, func() (json.RawMessage, error) {
		result, err := s.options.Runtime.GetPrompt(ctx, peer, name, arguments)
		if err != nil {
			ref.DeliveryFinished()
			return nil, err
		}
		return mcpruntime.TransformPromptResult(ctx, result, peer.Name, options)
	})
	if err != nil {
		ref.DeliveryFinished()
		return nil, err
	}
	content, err := read.Await(scope)
	if err != nil {
		return nil, err
	}
	var blocks []protocol.JSONObject
	err = json.Unmarshal(content, &blocks)
	return blocks, err
}
