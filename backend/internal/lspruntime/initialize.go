package lspruntime

import (
	"encoding/json"
	"path/filepath"
)

// InitializeParams is the original concrete UTF-16 client capability contract,
// shared by every plugin server and exercised against the original instance.
func InitializeParams(pid int, cwd string, initialization json.RawMessage) map[string]any {
	if initialization == nil || string(initialization) == "null" {
		initialization = json.RawMessage(`{}`)
	}
	return map[string]any{"processId": pid, "initializationOptions": initialization, "workspaceFolders": []any{map[string]any{"uri": URI(cwd), "name": filepath.Base(cwd)}}, "rootPath": cwd, "rootUri": URI(cwd), "capabilities": map[string]any{
		"workspace": map[string]any{"configuration": false, "workspaceFolders": false},
		"textDocument": map[string]any{
			"synchronization":    map[string]any{"dynamicRegistration": false, "willSave": false, "willSaveWaitUntil": false, "didSave": true},
			"publishDiagnostics": map[string]any{"relatedInformation": true, "tagSupport": map[string]any{"valueSet": []int{1, 2}}, "versionSupport": false, "codeDescriptionSupport": true, "dataSupport": false},
			"hover":              map[string]any{"dynamicRegistration": false, "contentFormat": []string{"markdown", "plaintext"}},
			"definition":         map[string]any{"dynamicRegistration": false, "linkSupport": true},
			"references":         map[string]any{"dynamicRegistration": false}, "documentSymbol": map[string]any{"dynamicRegistration": false, "hierarchicalDocumentSymbolSupport": true}, "callHierarchy": map[string]any{"dynamicRegistration": false},
		}, "general": map[string]any{"positionEncodings": []string{"utf-16"}},
	}}
}
