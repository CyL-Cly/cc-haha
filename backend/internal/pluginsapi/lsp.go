package pluginsapi

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginschema"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

// LSPDeclarations extracts the shared original declaration, preserving the
// strict-object refinements and their exact Zod-compatible issues.
func LSPDeclarations() (*pluginschema.Node, error) {
	schemas, err := pluginschema.Declarations()
	if err != nil {
		return nil, err
	}
	var node pluginschema.Node
	if err = json.Unmarshal(schemas.Manifest.Shape.Get("lspServers"), &node); err != nil {
		return nil, err
	}
	if node.InnerType == nil {
		return nil, fmt.Errorf("Missing shared LSP declaration")
	}
	for _, option := range node.InnerType.Options {
		if option.Type == "record" {
			return option, nil
		}
	}
	return nil, fmt.Errorf("Missing shared LSP record declaration")
}
func lspFailure(p pluginregistry.Loaded, server, message string) protocol.JSONObject {
	return rec(field("type", "lsp-config-invalid"), field("plugin", p.Name), field("serverName", server), field("validationError", message), field("source", "plugin"))
}
func lspRead(path string) (json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	data = protocol.DecodeJavaScriptUTF8(data)
	if !json.Valid(data) {
		message, _ := provider.PinnedJavaScriptJSONSyntaxMessage(string(data))
		return nil, fmt.Errorf("%s", stringValue(message))
	}
	return data, nil
}
func lspIOMessage(err error) string {
	var path *os.PathError
	if !asPathError(err, &path) {
		return err.Error()
	}
	code := "ENOENT"
	if os.IsPermission(err) {
		code = "EACCES"
	}
	if strings.Contains(err.Error(), "not a directory") {
		code = "ENOTDIR"
	}
	if strings.Contains(err.Error(), "is a directory") {
		code = "EISDIR"
	}
	detail := map[string]string{"ENOENT": "no such file or directory", "EACCES": "permission denied", "ENOTDIR": "not a directory", "EISDIR": "illegal operation on a directory"}[code]
	if code == "EISDIR" {
		return code + ": " + detail + ", read"
	}
	return code + ": " + detail + ", open '" + path.Path + "'"
}
func asPathError(err error, target **os.PathError) bool {
	value, ok := err.(*os.PathError)
	if ok {
		*target = value
	}
	return ok
}
func LoadLSP(ctx context.Context, p pluginregistry.Loaded, failures *[]protocol.JSONObject) (*protocol.JSONObject, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	schema, err := LSPDeclarations()
	if err != nil {
		return nil, err
	}
	servers := rec()
	file := func(path, name string, optional bool) {
		data, err := lspRead(path)
		if err != nil {
			if optional && os.IsNotExist(err) {
				return
			}
			*failures = append(*failures, lspFailure(p, name, "Failed to parse JSON: "+lspIOMessage(err)))
			return
		}
		valid, issues := pluginschema.Validate(schema, data)
		if len(issues) > 0 {
			*failures = append(*failures, lspFailure(p, name, pluginschema.ErrorJSON(issues, nil)))
			return
		}
		for _, entry := range obj(valid).OwnProperties() {
			_ = servers.SetPropertyJSON(entry.NameJSON, entry.Value)
		}
	}
	file(filepath.Join(p.Path, ".lsp.json"), ".lsp.json", true)
	declaration := p.Manifest.Get("lspServers")
	declarations := []json.RawMessage{}
	if len(declaration) > 0 {
		if declaration[0] == '[' {
			declarations = arr(declaration)
		} else {
			declarations = []json.RawMessage{declaration}
		}
	}
	for _, value := range declarations {
		if len(value) > 0 && value[0] == '"' {
			relative := stringValue(value)
			resolved := filepath.Clean(filepath.Join(p.Path, relative))
			if filepath.IsAbs(relative) {
				resolved = relative
			}
			root, _ := filepath.Abs(p.Path)
			resolved, _ = filepath.Abs(resolved)
			rel, _ := filepath.Rel(root, resolved)
			if strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
				*failures = append(*failures, lspFailure(p, relative, "Invalid path: must be relative and within plugin directory"))
				continue
			}
			file(resolved, relative, false)
		} else {
			for _, entry := range obj(value).OwnProperties() {
				name := stringValue(entry.NameJSON)
				valid, issues := pluginschema.Validate(schema.ValueType, entry.Value)
				if len(issues) > 0 {
					*failures = append(*failures, lspFailure(p, name, pluginschema.ErrorJSON(issues, nil)))
				} else {
					_ = servers.SetPropertyJSON(entry.NameJSON, valid)
				}
			}
		}
	}
	if len(servers.OwnProperties()) == 0 {
		return nil, nil
	}
	return &servers, nil
}
