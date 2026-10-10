package sdkhost

import (
	"sort"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// The final producer mirrors print.buildAllTools -> mergeAndFilterTools:
// first occurrence wins, builtin and MCP partitions remain contiguous, and
// each partition uses the original English locale comparison.
func mergeNativeToolPool(definitions []toolschema.Definition, tools []queryloop.RegisteredTool) ([]toolschema.Definition, []queryloop.RegisteredTool, error) {
	if len(definitions) != len(tools) {
		return nil, nil, cap("final executable tool pool declaration count")
	}
	type pair struct {
		definition toolschema.Definition
		tool       queryloop.RegisteredTool
	}
	builtins, mcp := []pair{}, []pair{}
	seen := map[string]bool{}
	for i, tool := range tools {
		if definitions[i].Name != tool.Name {
			return nil, nil, cap("final executable tool pool identity")
		}
		if seen[tool.Name] {
			continue
		}
		seen[tool.Name] = true
		item := pair{definitions[i], tool}
		if tool.IsMCP || strings.HasPrefix(tool.Name, "mcp__") {
			mcp = append(mcp, item)
		} else {
			builtins = append(builtins, item)
		}
	}
	comparator := collate.New(language.English)
	for _, partition := range [][]pair{builtins, mcp} {
		sort.SliceStable(partition, func(i, j int) bool {
			return comparator.CompareString(partition[i].tool.Name, partition[j].tool.Name) < 0
		})
	}
	outDefinitions, outTools := []toolschema.Definition{}, []queryloop.RegisteredTool{}
	for _, item := range append(builtins, mcp...) {
		outDefinitions = append(outDefinitions, item.definition)
		outTools = append(outTools, item.tool)
	}
	return outDefinitions, outTools, nil
}
