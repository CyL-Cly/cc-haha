package toolschema

// LSPDefinition is the native strict schema shared by parsing and the API tool.
func LSPDefinition() Definition {
	zero := 0.0
	coordinate := func(description string) Schema {
		return Schema{Kind: "number", Integer: true, ExclusiveMinimum: &zero, Description: quote(description)}
	}
	return Definition{Kind: "builtin", Name: "LSP", ShouldDefer: true, SearchHint: quote("code intelligence (definitions, references, symbols, hover)"), Description: quote(lspDescription), Prompt: quote(lspDescription), InputSchema: &Schema{Kind: "object", UnknownKeys: "strict", Fields: []Field{
		{"operation", Schema{Kind: "enum", Values: []string{"goToDefinition", "findReferences", "hover", "documentSymbol", "workspaceSymbol", "goToImplementation", "prepareCallHierarchy", "incomingCalls", "outgoingCalls"}, Description: quote("The LSP operation to perform")}},
		{"filePath", textSchema("The absolute or relative path to the file", false)},
		{"line", coordinate("The line number (1-based, as shown in editors)")},
		{"character", coordinate("The character offset (1-based, as shown in editors)")},
	}}}
}

const lspDescription = `Interact with Language Server Protocol (LSP) servers to get code intelligence features.

Supported operations:
- goToDefinition: Find where a symbol is defined
- findReferences: Find all references to a symbol
- hover: Get hover information (documentation, type info) for a symbol
- documentSymbol: Get all symbols (functions, classes, variables) in a document
- workspaceSymbol: Search for symbols across the entire workspace
- goToImplementation: Find implementations of an interface or abstract method
- prepareCallHierarchy: Get call hierarchy item at a position (functions/methods)
- incomingCalls: Find all functions/methods that call the function at a position
- outgoingCalls: Find all functions/methods called by the function at a position

All operations require:
- filePath: The file to operate on
- line: The line number (1-based, as shown in editors)
- character: The character offset (1-based, as shown in editors)

Note: LSP servers must be configured for the file type. If no server is available, an error will be returned.`
