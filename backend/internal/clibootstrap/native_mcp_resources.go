package clibootstrap

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/webfetch"
)

func (m *MainPrint) nativeMCPResources() *builtintools.MCPResourceBindings {
	if m.mcpTools == nil {
		return nil
	}
	return &builtintools.MCPResourceBindings{Available: m.mcpTools.ResourcesAvailable, List: m.mcpTools.ListResourceTool, Read: m.nativeReadMCPResource}
}

func nodeResourceBase64(value string) []byte {
	clean := strings.Builder{}
	for _, r := range value {
		if r == '=' {
			break
		}
		if r == '-' {
			r = '+'
		} else if r == '_' {
			r = '/'
		}
		if r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '+' || r == '/' {
			clean.WriteRune(r)
		}
	}
	text := clean.String()
	if len(text)%4 == 1 {
		text = text[:len(text)-1]
	}
	data, _ := base64.RawStdEncoding.DecodeString(text)
	return data
}

func (m *MainPrint) nativeReadMCPResource(ctx context.Context, scope *cooperative.Scope, server, uri string) (json.RawMessage, error) {
	result, e := m.mcpTools.ReadResourceToolRaw(ctx, scope, server, uri)
	if e != nil {
		return nil, e
	}
	var rows []protocol.JSONObject
	if e := json.Unmarshal(result.Get("contents"), &rows); e != nil {
		return nil, e
	}
	contents := make([]protocol.JSONObject, len(rows))
	type pendingBinary struct {
		index  int
		out    protocol.JSONObject
		row    protocol.JSONObject
		path   string
		mime   string
		size   int
		future *ownerasync.OwnerFuture[error]
	}
	pending := []pendingBinary{}
	for i, row := range rows {
		out := protocol.NewJSONObject(protocol.JSONField{Name: "uri", Value: row.Get("uri")}, protocol.JSONField{Name: "mimeType", Value: row.Get("mimeType")})
		if row.Get("text") != nil {
			out.Set("text", row.Get("text"))
			contents[i] = out
			continue
		}
		var blob string
		if e := json.Unmarshal(row.Get("blob"), &blob); e != nil {
			contents[i] = out
			continue
		}
		mime := row.String("mimeType")
		id := fmt.Sprintf("mcp-resource-%s-%d-%s", strings.TrimSuffix(fmt.Sprintf("%.0f", m.options.Native.NowMillis()), ".0"), i, webfetch.RandomBase36Suffix(nativeMathRandom()))
		data := nodeResourceBase64(blob)
		state := m.state.Snapshot()
		config := m.options.Native.Environment["CLAUDE_CONFIG_DIR"]
		if config == "" {
			config = filepath.Join(m.options.Native.Home, ".claude")
		}
		dir := filepath.Join(config, "projects", projectpath.Sanitize(state.OriginalCWD), m.Controller.SessionID(), "tool-results")
		path := filepath.Join(dir, id+"."+webfetch.ExtensionForMIME(mime))
		future, e := nativeSkillWork(m, scope, func() (error, error) {
			if e := context.Cause(ctx); e != nil {
				return nil, e
			}
			if e := m.options.Native.check("mkdir", dir); e != nil {
				return nil, e
			}
			// Source ensureToolResultsDir catches mkdir failures; the actual
			// write error below becomes the resource's binary-save explanation.
			_ = os.MkdirAll(dir, 0777)
			if e := m.options.Native.check("writeFile", path); e != nil {
				return e, nil
			}
			return os.WriteFile(path, data, 0666), nil
		})
		if e != nil {
			return nil, e
		}
		pending = append(pending, pendingBinary{index: i, out: out, row: row, path: path, mime: mime, size: len(data), future: future})
	}
	// The original Promise.all starts every blob write before waiting and
	// retains the incoming contents order despite completion order.
	for _, item := range pending {
		failure, e := item.future.Await(scope)
		if e != nil {
			return nil, e
		}
		if failure != nil {
			item.out.Set("text", quoteMain("Binary content could not be saved to disk: "+strings.TrimPrefix(storage.JavaScriptErrorText(failure), "Error: ")))
		} else {
			item.out.Set("blobSavedTo", quoteMain(item.path))
			mimeText := item.row.Get("mimeType")
			if item.mime == "" {
				mimeText = quoteMain("unknown type")
			}
			explanation, e := protocol.JoinJavaScriptValuesJSON([]json.RawMessage{quoteMain("[Resource from " + server + " at "), item.row.Get("uri"), quoteMain("] Binary content ("), mimeText, quoteMain(", " + webfetch.FormatSize(item.size) + ") saved to " + item.path)}, "")
			if e != nil {
				return nil, e
			}
			item.out.Set("text", explanation)
		}
		contents[item.index] = item.out
	}
	return rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "contents", Value: rawMain(contents)})), nil
}
