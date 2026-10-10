package agentcatalog

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/markdownfrontmatter"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Frontmatter shares the native parser used by Skill/plugin configuration.
// It preserves the original failed-YAML fallback and ordered unknown fields.
func Frontmatter(markdown string) (protocol.JSONObject, string, error) {
	parsed, err := markdownfrontmatter.Parse(markdown)
	return parsed.Frontmatter, parsed.Content, err
}
