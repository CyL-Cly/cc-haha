package builtintools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/lspruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func (s *Service) lspAvailable() bool {
	return s.options.LSP != nil && s.options.LSP.Owns(s.options.Owner, s.options.NativeBinding) && s.options.LSP.CatalogEnabled() && taskEnvironmentTruthy(s.options.Environment["ENABLE_LSP_TOOL"])
}
func (s *Service) lspCatalog() (toolschema.Definition, queryloop.RegisteredTool) {
	d := toolschema.LSPDefinition()
	t := queryloop.RegisteredTool{Name: "LSP", ShouldDefer: true, SearchHint: d.SearchHint, PersistenceThreshold: 100000, IsConcurrencySafe: func(protocol.JSONObject) bool { return true }, ParseInput: func(in protocol.JSONObject) (protocol.JSONObject, error) { return toolschema.ParseInput(d, in) }, ValidateInput: func(ctx context.Context, in protocol.JSONObject, opaque any) error {
		return s.validateLSP(ctx, in, scopeFor(opaque))
	}, StartCallJSON: s.startLSP, MapResultJSON: mapLSPResult}
	t.CallJSON = func(ctx context.Context, in protocol.JSONObject, c queryloop.JSONToolCallContext) (queryloop.ToolCallResult, error) {
		f, e := s.startLSP(ctx, in, c)
		if e != nil {
			return queryloop.ToolCallResult{}, e
		}
		return f.Await(scopeFor(c.Opaque))
	}
	return d, t
}
func (s *Service) validateLSP(ctx context.Context, in protocol.JSONObject, scope *cooperative.Scope) error {
	if err := s.check(scope); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := toolschema.ParseInput(toolschema.LSPDefinition(), in); err != nil {
		return &builtinwrite.ValidationError{Message: "Invalid input: " + err.Error(), ErrorCode: 3}
	}
	path, err := s.path(in, "filePath")
	if err != nil {
		return err
	}
	future, err := native(s, scope, func() (os.FileInfo, error) {
		info, e := os.Stat(path)
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			s.options.LSP.ReportError(fmt.Errorf("Failed to access file stats for LSP operation on %s: %v", in.String("filePath"), e))
		}
		return info, e
	})
	if err != nil {
		return err
	}
	info, err := future.Await(scope)
	if errors.Is(err, os.ErrNotExist) {
		return &builtinwrite.ValidationError{Message: "File does not exist: " + in.String("filePath"), ErrorCode: 1}
	}
	if err != nil {
		return &builtinwrite.ValidationError{Message: "Cannot access file: " + in.String("filePath") + ". " + err.Error(), ErrorCode: 4}
	}
	if !info.Mode().IsRegular() {
		return &builtinwrite.ValidationError{Message: "Path is not a file: " + in.String("filePath"), ErrorCode: 2}
	}
	return nil
}
func (s *Service) startLSP(ctx context.Context, in protocol.JSONObject, c queryloop.JSONToolCallContext) (*ownerasync.OwnerFuture[queryloop.ToolCallResult], error) {
	scope := scopeFor(c.Opaque)
	if err := s.check(scope); err != nil {
		return nil, err
	}
	if s.options.LSP == nil || !s.options.LSP.Owns(s.options.Owner, s.options.NativeBinding) {
		return nil, capability("actual shared LSP runtime")
	}
	path, err := s.path(in, "filePath")
	if err != nil {
		return nil, err
	}
	manager, cwd, env := s.options.LSP, s.options.CWD, map[string]string{}
	for k, v := range s.options.Environment {
		env[k] = v
	}
	input := in.Clone()
	future, err := native(s, scope, func() (json.RawMessage, error) { return runLSP(ctx, manager, input, path, cwd, env) })
	if err != nil {
		return nil, err
	}
	return transform(s, scope, future, func(_ *cooperative.Scope, data json.RawMessage) (queryloop.ToolCallResult, error) {
		return queryloop.ToolCallResult{Data: data}, nil
	})
}
func runLSP(ctx context.Context, manager *lspruntime.Manager, in protocol.JSONObject, path, cwd string, env map[string]string) (json.RawMessage, error) {
	operation := in.String("operation")
	output := protocol.NewJSONObject(field("operation", operation), protocol.JSONField{Name: "filePath", Value: in.Get("filePath")})
	finish := func(message json.RawMessage, counts ...int) (json.RawMessage, error) {
		output.Set("result", message)
		if len(counts) == 2 {
			output.Set("resultCount", raw(counts[0]))
			output.Set("fileCount", raw(counts[1]))
		}
		ordered := protocol.NewJSONObject(field("operation", operation), protocol.JSONField{Name: "result", Value: message}, protocol.JSONField{Name: "filePath", Value: in.Get("filePath")})
		for _, key := range []string{"resultCount", "fileCount"} {
			if v := output.Get(key); v != nil {
				ordered.Set(key, v)
			}
		}
		return ordered.MarshalJSON()
	}
	if err := manager.Wait(ctx); err != nil {
		return finish(quote("LSP server manager not initialized. This may indicate a startup issue."))
	}
	perform := func() (json.RawMessage, error) {
		if !manager.IsOpen(path) {
			handle, err := os.Open(path)
			if err != nil {
				return nil, err
			}
			defer handle.Close()
			info, err := handle.Stat()
			if err != nil {
				return nil, err
			}
			if info.Size() > 10000000 {
				return quote(fmt.Sprintf("File too large for LSP analysis (%dMB exceeds 10MB limit)", int(math.Ceil(float64(info.Size())/1000000)))), nil
			}
			data, err := io.ReadAll(handle)
			if err != nil {
				return nil, err
			}
			if err = manager.Open(path, quote(strings.ToValidUTF8(string(data), "\ufffd"))); err != nil {
				return nil, err
			}
		}
		method := "textDocument/" + map[string]string{"goToDefinition": "definition", "findReferences": "references", "hover": "hover", "documentSymbol": "documentSymbol", "workspaceSymbol": "symbol", "goToImplementation": "implementation", "prepareCallHierarchy": "prepareCallHierarchy", "incomingCalls": "prepareCallHierarchy", "outgoingCalls": "prepareCallHierarchy"}[operation]
		params := map[string]any{"textDocument": map[string]any{"uri": lspruntime.URI(path)}, "position": map[string]int{"line": number(in, "line", 1) - 1, "character": number(in, "character", 1) - 1}}
		if operation == "findReferences" {
			params["context"] = map[string]bool{"includeDeclaration": true}
		}
		if operation == "documentSymbol" {
			delete(params, "position")
		}
		if operation == "workspaceSymbol" {
			method = "workspace/symbol"
			params = map[string]any{"query": ""}
		}
		result, found, err := manager.Request(path, method, params)
		if err != nil {
			return nil, err
		}
		if !found {
			return quote("No LSP server available for file type: " + filepath.Ext(path)), nil
		}
		if operation == "incomingCalls" || operation == "outgoingCalls" {
			var items []json.RawMessage
			_ = json.Unmarshal(result, &items)
			if len(items) == 0 {
				output.Set("resultCount", raw(0))
				output.Set("fileCount", raw(0))
				return quote("No call hierarchy item found at this position"), nil
			}
			result, _, err = manager.Request(path, "callHierarchy/"+operation, map[string]any{"item": items[0]})
			if err != nil {
				return nil, err
			}
		}
		result = filterLSPIgnored(ctx, result, operation, cwd, env)
		formatted, n, f := lspruntime.FormatResult(operation, result, cwd)
		output.Set("resultCount", raw(n))
		output.Set("fileCount", raw(f))
		return formatted, nil
	}
	formatted, err := perform()
	if err != nil {
		manager.ReportError(fmt.Errorf("LSP tool request failed for %s on %s: %v", operation, in.String("filePath"), err))
		return finish(quote("Error performing " + operation + ": " + err.Error()))
	}
	return finish(formatted)
}
func filterLSPIgnored(ctx context.Context, result json.RawMessage, operation, cwd string, env map[string]string) json.RawMessage {
	if operation != "findReferences" && operation != "goToDefinition" && operation != "goToImplementation" && operation != "workspaceSymbol" {
		return result
	}
	var rows []json.RawMessage
	if json.Unmarshal(result, &rows) != nil || len(rows) == 0 {
		return result
	}
	paths, uris := []string{}, map[string]string{}
	uriFor := func(r json.RawMessage) string {
		var o protocol.JSONObject
		_ = o.UnmarshalJSON(r)
		if operation == "workspaceSymbol" {
			var l protocol.JSONObject
			_ = l.UnmarshalJSON(o.Get("location"))
			return l.String("uri")
		}
		if o.Get("targetUri") != nil {
			return o.String("targetUri")
		}
		return o.String("uri")
	}
	seen := map[string]bool{}
	for _, r := range rows {
		uri := uriFor(r)
		if uri != "" {
			p := lspruntime.URIPath(uri)
			uris[uri] = p
			if !seen[p] {
				paths = append(paths, p)
				seen[p] = true
			}
		}
	}
	ignored := map[string]bool{}
	for i := 0; i < len(paths); i += 50 {
		end := i + 50
		if end > len(paths) {
			end = len(paths)
		}
		batch, cancel := context.WithTimeout(ctx, 5*time.Second)
		cmd := exec.CommandContext(batch, "git", append([]string{"check-ignore"}, paths[i:end]...)...)
		cmd.Dir = cwd
		cmd.Env = []string{}
		for k, v := range env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		out, err := cmd.Output()
		cancel()
		if err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				line = protocol.TrimJavaScriptString(line)
				if line != "" {
					ignored[line] = true
				}
			}
		}
	}
	if len(ignored) == 0 {
		return result
	}
	filtered := []json.RawMessage{}
	for _, r := range rows {
		if !ignored[uris[uriFor(r)]] {
			filtered = append(filtered, r)
		}
	}
	return raw(filtered)
}
func mapLSPResult(data, id json.RawMessage) (protocol.JSONObject, error) {
	var o protocol.JSONObject
	if err := o.UnmarshalJSON(data); err != nil {
		return protocol.JSONObject{}, err
	}
	return protocol.NewJSONObject(protocol.JSONField{Name: "tool_use_id", Value: id}, field("type", "tool_result"), protocol.JSONField{Name: "content", Value: o.Get("result")}), nil
}
