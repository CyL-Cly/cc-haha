package builtintools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
)

type skillMentionStat struct {
	directory bool
	mtime     float64
	size      int64
	names     []string
}

// SkillFileAttachment uses the original shared Read cache, permission matcher
// and tool call. The calling prompt producer supplies the same Owner Scope;
// this helper neither grants a permission nor constructs another tool service.
func (s *Service) SkillFileAttachment(ctx context.Context, scope *cooperative.Scope, mention skillcommands.MentionedFile) (json.RawMessage, error) {
	if e := s.check(scope); e != nil {
		return nil, e
	}
	path, e := s.expand(quote(mention.Filename))
	if e != nil {
		return nil, e
	}
	permission, e := s.options.PermissionContext(scope)
	if e != nil {
		return nil, e
	}
	denied, e := s.matchPathRule(permission, "alwaysDenyRules", "Read", path, false)
	if e != nil || denied {
		return nil, e
	}
	stat, e := native(s, scope, func() (skillMentionStat, error) {
		info, e := os.Stat(path)
		if e != nil {
			return skillMentionStat{}, e
		}
		out := skillMentionStat{directory: info.IsDir(), mtime: float64(info.ModTime().UnixMilli()), size: info.Size()}
		if info.IsDir() {
			file, e := os.Open(path)
			if e != nil {
				return out, e
			}
			defer file.Close()
			entries, e := file.ReadDir(-1)
			if e != nil {
				return out, e
			}
			for i, entry := range entries {
				if i >= 1000 {
					out.names = append(out.names, fmt.Sprintf("… and %d more entries", len(entries)-1000))
					break
				}
				out.names = append(out.names, entry.Name())
			}
		}
		return out, nil
	})
	if e != nil {
		return nil, e
	}
	info, e := stat.Await(scope)
	if e != nil {
		if context.Cause(ctx) != nil {
			return nil, context.Cause(ctx)
		}
		return nil, nil
	}
	display, e := filepath.Rel(s.options.CWD, path)
	if e != nil {
		return nil, e
	}
	if info.directory {
		return object(field("type", "directory"), field("path", path), field("content", strings.Join(info.names, "\n")), field("displayPath", display)), nil
	}
	if info.size > 256*1024 {
		if strings.EqualFold(filepath.Ext(path), ".pdf") {
			return nil, capability("Skill mention actual PDF page/reference producer")
		}
		return nil, nil
	}
	cached, present, e := s.options.Cache.Get(scope, path)
	if e != nil {
		return nil, e
	}
	if present && cached.Timestamp == info.mtime {
		var text string
		if e := json.Unmarshal(cached.Content, &text); e != nil {
			return nil, e
		}
		start := 1.0
		if mention.LineStart != nil {
			start = *mention.LineStart
		}
		lines := strings.Count(text, "\n") + 1
		content := object(field("type", "text"), protocol.JSONField{Name: "file", Value: object(field("filePath", path), protocol.JSONField{Name: "content", Value: cached.Content}, field("numLines", lines), field("startLine", start), field("totalLines", lines))})
		return object(field("type", "already_read_file"), field("filename", path), field("displayPath", display), protocol.JSONField{Name: "content", Value: content}), nil
	}
	in := protocol.NewJSONObject(field("file_path", path))
	if mention.LineStart != nil {
		in.Set("offset", raw(*mention.LineStart))
	}
	if mention.LineEnd != nil && mention.LineStart != nil && *mention.LineEnd != 0 && *mention.LineStart != 0 {
		in.Set("limit", raw(*mention.LineEnd-*mention.LineStart+1))
	}
	if e = s.Validate(ctx, "Read", in, scope); e != nil {
		return nil, skillAttachmentFailure(e)
	}
	data, e := s.Call(ctx, "Read", in, scope, false)
	truncated := false
	if e != nil && strings.HasPrefix(e.Error(), "File content (") && strings.Contains(e.Error(), "exceeds maximum allowed tokens") {
		in.Set("limit", raw(2000))
		data, e = s.Call(ctx, "Read", in, scope, false)
		truncated = e == nil
	}
	if e != nil {
		return nil, skillAttachmentFailure(e)
	}
	var content protocol.JSONObject
	if content.UnmarshalJSON(data) == nil && content.String("type") == "image" && s.options.Images != nil && s.options.Images.RegisterUserImage != nil {
		if err := s.options.Images.RegisterUserImage(ctx, scope, path); err != nil {
			return nil, err
		}
	}
	fields := []protocol.JSONField{field("type", "file"), field("filename", path), {Name: "content", Value: data}}
	if truncated {
		fields = append(fields, field("truncated", true))
	}
	fields = append(fields, field("displayPath", display))
	return object(fields...), nil
}

func skillAttachmentFailure(e error) error {
	var unsupported *CapabilityError
	if errors.As(e, &unsupported) || errors.Is(e, cooperative.ErrRestricted) || errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
		return e
	}
	// Original attachment discovery drops ordinary validation/read failures.
	// Migration capability and ownership errors remain explicit.
	return nil
}
