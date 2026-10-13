package builtintools

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/preflightcore"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"path/filepath"
	"sort"
	"strings"
)

// RestoreCompactFiles uses the actual live Read cache and physical Read path,
// retaining its deny/symlink validation. Snapshot/clear is delayed until model
// success; the bounded fresh attachments cannot reintroduce stale file text.
func (s *Service) RestoreCompactFiles(ctx context.Context, scope *cooperative.Scope, keep []protocol.JSONObject, factory *messages.Factory, excludedPaths ...string) ([]protocol.JSONObject, error) {
	if e := s.check(scope); e != nil {
		return nil, e
	}
	cached, e := s.options.Cache.CompactEntries(scope)
	if e != nil {
		return nil, e
	}
	if e = s.options.Cache.ClearAfterCompact(scope); e != nil {
		return nil, e
	}
	excluded := map[string]bool{}
	for _, path := range excludedPaths {
		excluded[filepath.Clean(path)] = true
	}
	stubIDs := map[string]bool{}
	for _, m := range keep {
		if m.String("type") != "user" {
			continue
		}
		api, _ := m.Object("message")
		var blocks []protocol.JSONObject
		_ = json.Unmarshal(api.Get("content"), &blocks)
		for _, block := range blocks {
			if block.String("type") == "tool_result" && strings.HasPrefix(block.String("content"), unchanged) {
				stubIDs[block.String("tool_use_id")] = true
			}
		}
	}
	preserved := map[string]bool{}
	for _, m := range keep {
		api, _ := m.Object("message")
		var blocks []protocol.JSONObject
		_ = json.Unmarshal(api.Get("content"), &blocks)
		for _, b := range blocks {
			if b.String("type") == "tool_use" && b.String("name") == "Read" && !stubIDs[b.String("id")] {
				in, _ := b.Object("input")
				path, _ := s.path(in, "file_path")
				preserved[path] = true
			}
		}
	}
	sort.SliceStable(cached, func(i, j int) bool { return cached[i].State.Timestamp > cached[j].State.Timestamp })
	out := []protocol.JSONObject{}
	used := float64(0)
	selected := 0
	for _, file := range cached {
		if preserved[file.Path] || excluded[file.Path] {
			continue
		}
		selected++
		if selected > 5 {
			break
		}
		if ctx.Err() != nil {
			return nil, context.Cause(ctx)
		}
		input := protocol.NewJSONObject(protocol.JSONField{Name: "file_path", Value: jsonBytes(file.Path)})
		if e = s.validateRead(input, scope, file.Path); e != nil {
			continue
		}
		pending, err := s.startReadBodyWithLimit(ctx, input, scope, false, 5000)
		if err != nil {
			return nil, err
		}
		data, err := pending.Await(scope)
		display, _ := filepath.Rel(s.options.CWD, file.Path)
		body := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: jsonBytes("file")}, protocol.JSONField{Name: "filename", Value: jsonBytes(file.Path)}, protocol.JSONField{Name: "content", Value: data}, protocol.JSONField{Name: "displayPath", Value: jsonBytes(display)})
		if err != nil && (strings.HasPrefix(err.Error(), "File content (") && strings.Contains(err.Error(), "exceeds maximum allowed tokens") || strings.HasPrefix(err.Error(), "File size (") || strings.Contains(err.Error(), "maximum allowed size")) {
			body = protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: jsonBytes("compact_file_reference")}, protocol.JSONField{Name: "filename", Value: jsonBytes(file.Path)}, protocol.JSONField{Name: "displayPath", Value: jsonBytes(display)})
		} else if err != nil {
			if failure := skillAttachmentFailure(err); failure != nil {
				return nil, failure
			}
			continue
		}
		attachment := factory.CreateAttachment(jsonBytes(body))
		serialized, err := attachment.MarshalJSON()
		if err != nil {
			return nil, err
		}
		tokens, err := preflightcore.RoughContent(jsonBytes(string(serialized)))
		if err != nil {
			return nil, err
		}
		if used+tokens > 50000 {
			continue
		}
		used += tokens
		out = append(out, attachment)
	}
	return out, nil
}
func jsonBytes(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

func (s *Service) ClearCompactCache(scope *cooperative.Scope) error {
	if e := s.check(scope); e != nil {
		return e
	}
	return s.options.Cache.ClearAfterCompact(scope)
}
