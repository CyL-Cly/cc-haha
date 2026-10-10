package toolschema

import _ "embed"

//go:embed assets/list-mcp-resources.md
var listMCPResourcesPrompt string

//go:embed assets/read-mcp-resource.md
var readMCPResourcePrompt string

func mcpResourceFields(name string) []Field {
	if name == "ListMcpResourcesTool" {
		return []Field{{"server", textSchema("Optional server name to filter resources by", true)}}
	}
	return []Field{{"server", textSchema("The MCP server name", false)}, {"uri", textSchema("The resource URI to read", false)}}
}
