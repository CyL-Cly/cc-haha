package mcpruntime

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/textsanitization"
)

type promptDiscovery struct {
	done chan struct{}
	rows []protocol.JSONObject
}

// FetchPrompts is Source fetchCommandsForClient's independent 20-entry LRU.
// It requests one schema-validated page only; unsupported capabilities and
// caught peer/schema failures produce the original cached empty inventory.
func (s *Service) FetchPrompts(ctx context.Context, peer Connection) ([]protocol.JSONObject, error) {
	if !s.OwnsConnection(peer) {
		return nil, errors.New("MCP prompt peer belongs to a different process graph")
	}
	if peer.Status != "connected" || peer.Client == nil || peer.Client.Capabilities().Get("prompts") == nil {
		return []protocol.JSONObject{}, nil
	}
	owner := peer.runtime
	key := owner.key(peer.Name, peer.Config)
	owner.mu.Lock()
	if owner.closed {
		owner.mu.Unlock()
		return nil, context.Canceled
	}
	if owner.promptCache == nil {
		owner.promptCache = map[string]*promptDiscovery{}
		owner.promptReads = map[*promptDiscovery]bool{}
	}
	read := owner.promptCache[key]
	if read == nil {
		read = &promptDiscovery{done: make(chan struct{}), rows: []protocol.JSONObject{}}
		owner.promptCache[key] = read
		owner.promptReads[read] = true
		go func() {
			defer func() { owner.mu.Lock(); delete(owner.promptReads, read); owner.mu.Unlock() }()
			result, err := peer.Client.ListPrompts(context.Background(), nil)
			if err == nil {
				data, err := textsanitization.Recursive(result.Get("prompts"))
				if err == nil {
					_ = json.Unmarshal(data, &read.rows)
				}
			}
			close(read.done)
		}()
	}
	if at := slices.Index(owner.promptOrder, key); at >= 0 {
		owner.promptOrder = append(owner.promptOrder[:at], owner.promptOrder[at+1:]...)
	}
	owner.promptOrder = append(owner.promptOrder, key)
	if len(owner.promptOrder) > 20 {
		delete(owner.promptCache, owner.promptOrder[0])
		owner.promptOrder = owner.promptOrder[1:]
	}
	owner.mu.Unlock()
	select {
	case <-read.done:
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
	return clonePromptRows(read.rows), nil
}

func clonePromptRows(rows []protocol.JSONObject) []protocol.JSONObject {
	out := make([]protocol.JSONObject, len(rows))
	for i, row := range rows {
		out[i] = row.Clone()
	}
	return out
}

func (s *Service) InvalidatePrompts(peer Connection) error {
	if !s.OwnsConnection(peer) {
		return errors.New("MCP prompt invalidation belongs to a different process graph")
	}
	owner := peer.runtime
	owner.mu.Lock()
	owner.forgetPromptsLocked(owner.key(peer.Name, peer.Config))
	owner.mu.Unlock()
	return nil
}

func (s *Service) forgetPromptsLocked(key string) {
	delete(s.promptCache, key)
	if at := slices.Index(s.promptOrder, key); at >= 0 {
		s.promptOrder = append(s.promptOrder[:at], s.promptOrder[at+1:]...)
	}
}

// GetPrompt reuses the actual connection's namespace and credential/policy
// producer, including reconnection after a transport close. It never starts an
// OAuth interaction or bypasses a disabled peer.
func (s *Service) GetPrompt(ctx context.Context, peer Connection, name string, arguments protocol.JSONObject) (protocol.JSONObject, error) {
	connected, err := s.RefreshConnection(ctx, peer)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if connected.Status != "connected" || connected.Client == nil {
		return protocol.JSONObject{}, errors.New("MCP prompt server is not connected")
	}
	return connected.Client.GetPrompt(ctx, name, arguments)
}

// Source transforms each prompt message's content then flattens it, ignoring
// server roles. The same native image/blob producer is used for tools/prompts.
func TransformPromptResult(ctx context.Context, result protocol.JSONObject, server string, options RichResultOptions) (json.RawMessage, error) {
	var messages []protocol.JSONObject
	if err := json.Unmarshal(result.Get("messages"), &messages); err != nil {
		if options.OnSettled != nil {
			options.OnSettled()
		}
		return nil, err
	}
	blocks := []json.RawMessage{}
	for _, message := range messages {
		blocks = append(blocks, message.Get("content"))
	}
	return TransformRichResult(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "content", Value: resultRaw(blocks)}), "", server, options)
}
