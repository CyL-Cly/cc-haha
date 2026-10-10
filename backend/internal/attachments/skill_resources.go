package attachments

import (
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func projectSkillResource(value protocol.JSONObject, factory *messages.Factory) ([]protocol.JSONObject, error) {
	if factory == nil {
		return nil, errors.New("MCP resource attachment requires its owning message factory")
	}
	content, _ := object(value.Get("content"))
	rows := []json.RawMessage{}
	if content.IsObject() && content.Get("contents") != nil {
		if e := json.Unmarshal(content.Get("contents"), &rows); e != nil {
			return nil, &CapabilityError{Boundary: "malformed resource contents collection"}
		}
	}
	blocks := []protocol.JSONObject{}
	for _, raw := range rows {
		item, e := object(raw)
		if e != nil || !item.IsObject() {
			continue
		}
		if text := item.Get("text"); len(text) > 0 && text[0] == '"' {
			for _, part := range []json.RawMessage{quoted("Full contents of resource:"), text, quoted("Do NOT read this resource again unless you think it may have changed, since you already have the full contents.")} {
				blocks = append(blocks, protocol.NewJSONObject(field("type", quoted("text")), field("text", part)))
			}
		} else if item.Get("blob") != nil {
			mime := quoted("application/octet-stream")
			if item.Get("mimeType") != nil {
				mime, e = protocol.JavaScriptStringJSON(item.Get("mimeType"))
				if e != nil {
					return nil, e
				}
			}
			caption, e := concatenate(quoted("[Binary content: "), mime, quoted("]"))
			if e != nil {
				return nil, e
			}
			blocks = append(blocks, protocol.NewJSONObject(field("type", quoted("text")), field("text", caption)))
		}
	}
	var rendered json.RawMessage
	if len(blocks) > 0 {
		rendered, _ = json.Marshal(blocks)
	} else {
		server, e := protocol.JavaScriptStringJSON(value.Get("server"))
		if e != nil {
			return nil, e
		}
		uri, e := protocol.JavaScriptStringJSON(value.Get("uri"))
		if e != nil {
			return nil, e
		}
		note := "(No content)"
		if len(rows) > 0 {
			note = "(No displayable content)"
		}
		rendered, e = concatenate(quoted(`<mcp-resource server="`), server, quoted(`" uri="`), uri, quoted(`">`+note+`</mcp-resource>`))
		if e != nil {
			return nil, e
		}
	}
	user := messages.OrderedUser(factory.CreateUser(storage.Object{"content": rendered, "isMeta": json.RawMessage("true")}))
	api, _ := user.Object("message")
	if len(blocks) == 0 {
		wrapped, e := concatenate(quoted("<system-reminder>\n"), rendered, quoted("\n</system-reminder>"))
		if e != nil {
			return nil, e
		}
		api.Set("content", wrapped)
	} else {
		for i := range blocks {
			wrapped, e := concatenate(quoted("<system-reminder>\n"), blocks[i].Get("text"), quoted("\n</system-reminder>"))
			if e != nil {
				return nil, e
			}
			blocks[i].Set("text", wrapped)
		}
		rendered, _ = json.Marshal(blocks)
		api.Set("content", rendered)
	}
	raw, _ := api.MarshalJSON()
	user.Set("message", raw)
	return []protocol.JSONObject{user}, nil
}
