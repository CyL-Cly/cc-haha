package mcpapi

import "github.com/NanmiCoder/cc-haha/backend/internal/sourcecollation"

// Keep the original MCP-local entry point while sharing only the native
// Unicode collation primitive with other Source localeCompare consumers.
type nameComparator = sourcecollation.Comparator

func newNameComparator() (*nameComparator, error) { return sourcecollation.New() }
