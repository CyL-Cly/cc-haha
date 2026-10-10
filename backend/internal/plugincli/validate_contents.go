package plugincli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/NanmiCoder/cc-haha/backend/internal/pluginschema"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"go.yaml.in/yaml/v3"
)

func ValidatePluginContents(ctx context.Context, dir string) ([]ValidationResult, error) {
	results := []ValidationResult{}
	for _, kind := range []string{"skill", "agent", "command"} {
		folder := map[string]string{"skill": "skills", "agent": "agents", "command": "commands"}[kind]
		files, err := validationMarkdownFiles(filepath.Join(dir, folder), kind == "skill")
		if err != nil {
			return nil, err
		}
		for _, p := range files {
			if err = ctx.Err(); err != nil {
				return nil, err
			}
			content, e := os.ReadFile(p)
			if os.IsNotExist(e) {
				continue
			}
			var r ValidationResult
			if e != nil {
				r = validationResult(p, kind)
				r.addError("file", "Failed to read: "+validationIOError(e).Error(), "")
			} else {
				r = validateComponent(p, string(content), kind)
			}
			if len(r.Errors) > 0 || len(r.Warnings) > 0 {
				results = append(results, r)
			}
		}
	}
	hooks, err := validateHooksFile(filepath.Join(dir, "hooks", "hooks.json"))
	if err != nil {
		return nil, err
	}
	if len(hooks.Errors) > 0 || len(hooks.Warnings) > 0 {
		results = append(results, hooks)
	}
	return results, nil
}
func validationMarkdownFiles(dir string, skills bool) ([]string, error) {
	f, err := os.Open(dir)
	if os.IsNotExist(err) || errors.Is(err, syscall.ENOTDIR) {
		return []string{}, nil
	}
	if err != nil {
		if failure, ok := err.(*os.PathError); ok {
			copy := *failure
			copy.Op = "scandir"
			err = &copy
		}
		return nil, validationIOError(err)
	}
	entries, err := f.ReadDir(-1)
	_ = f.Close()
	if errors.Is(err, syscall.ENOTDIR) {
		return []string{}, nil
	}
	if err != nil {
		return nil, validationIOError(err)
	}
	files := []string{}
	for _, entry := range entries {
		p := filepath.Join(dir, entry.Name())
		if entry.IsDir() {
			if skills {
				files = append(files, filepath.Join(p, "SKILL.md"))
			} else {
				nested, e := validationMarkdownFiles(p, false)
				if e != nil {
					return nil, e
				}
				files = append(files, nested...)
			}
		} else if !skills && entry.Type().IsRegular() && strings.HasSuffix(strings.ToLower(entry.Name()), ".md") {
			files = append(files, p)
		}
	}
	return files, nil
}

func validateHooksFile(p string) (ValidationResult, error) {
	r := validationResult(p, "hooks")
	content, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return r, nil
	}
	if err != nil {
		r.addError("file", "Failed to read file: "+validationIOError(err).Error(), "")
		return r, nil
	}
	parsed, err := validationJSON(content)
	if err != nil {
		r.addError("json", "Invalid JSON syntax: "+err.Error()+". At runtime this breaks the entire plugin load.", "")
		return r, nil
	}
	schemas, err := pluginschema.Declarations()
	if err != nil {
		return r, err
	}
	schema := &pluginschema.Node{Type: "object", Shape: record(field("description", pluginschema.Node{Type: "optional", InnerType: &pluginschema.Node{Type: "string"}}), field("hooks", schemas.Hooks))}
	_, issues := pluginschema.Validate(schema, parsed)
	appendSchemaIssues(&r, issues)
	return r, nil
}

func validateComponent(p, content, kind string) ValidationResult {
	r := validationResult(p, kind)
	// This linter uses the same delimiter regex, but deliberately does not use
	// the runtime loader's YAML repair and empty-metadata fallback.
	match := regexp.MustCompile(`(?m)^---[\t ]*\r?\n([\s\S]*?)^---[\t ]*(?:\r?\n[\t \r\n]*|$)`).FindStringSubmatch(content)
	if match == nil {
		r.warn("frontmatter", "No frontmatter block found. Add YAML frontmatter between --- delimiters at the top of the file to set description and other metadata.")
		return r
	}
	fm, err := validationYAML(match[1])
	if err != nil {
		r.addError("frontmatter", "YAML frontmatter failed to parse: "+err.Error()+". At runtime this "+kind+" loads with empty metadata (all frontmatter fields silently dropped).", "")
		return r
	}
	typ := validationYAMLType(fm)
	if typ != "object" {
		label := typ
		if typ == "array" {
			label = "an array"
		}
		r.addError("frontmatter", "Frontmatter must be a YAML mapping (key: value pairs), got "+label+".", "")
		return r
	}
	fields := validationYAMLFields(fm)
	if d, ok := fields["description"]; ok {
		typ := validationYAMLType(d)
		if typ != "string" && typ != "number" && typ != "boolean" && typ != "null" {
			r.addError("description", "description must be a string, got "+typ+". At runtime this value is dropped.", "")
		}
	} else {
		r.warn("description", "No description in frontmatter. A description helps users and Claude understand when to use this "+kind+".")
	}
	if name := fields["name"]; name != nil {
		typ := validationYAMLType(name)
		if typ != "null" && typ != "string" {
			if typ == "array" {
				typ = "object"
			}
			r.addError("name", "name must be a string, got "+typ+".", "")
		}
	}
	if at := fields["allowed-tools"]; at != nil {
		typ := validationYAMLType(at)
		if typ != "null" && typ != "string" {
			if typ != "array" {
				r.addError("allowed-tools", "allowed-tools must be a string or array of strings, got "+typ+".", "")
			} else {
				for _, item := range validationYAMLNode(at).Content {
					if validationYAMLType(item) != "string" {
						r.addError("allowed-tools", "allowed-tools array must contain only strings.", "")
						break
					}
				}
			}
		}
	}
	if shell := fields["shell"]; shell != nil {
		typ := validationYAMLType(shell)
		if typ != "null" {
			if typ != "string" {
				if typ == "array" {
					typ = "object"
				}
				r.addError("shell", "shell must be a string, got "+typ+".", "")
			} else {
				text := validationYAMLNode(shell).Value
				normalized := strings.ToLower(protocol.TrimJavaScriptString(text))
				if normalized != "bash" && normalized != "powershell" {
					r.addError("shell", "shell must be 'bash' or 'powershell', got '"+text+"'.", "")
				}
			}
		}
	}
	return r
}
func validationYAMLNode(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}
func validationYAMLFields(n *yaml.Node) map[string]*yaml.Node {
	n = validationYAMLNode(n)
	fields := map[string]*yaml.Node{}
	if n == nil || n.Kind != yaml.MappingNode {
		return fields
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value != "<<" {
			fields[validationYAMLKey(n.Content[i])] = n.Content[i+1]
		}
	}
	merge := func(source *yaml.Node) {
		for key, value := range validationYAMLFields(source) {
			if _, present := fields[key]; !present {
				fields[key] = value
			}
		}
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value != "<<" {
			continue
		}
		source := validationYAMLNode(n.Content[i+1])
		if source.Kind == yaml.SequenceNode {
			for _, item := range source.Content {
				merge(item)
			}
		} else {
			merge(source)
		}
	}
	return fields
}
func validationYAMLKey(n *yaml.Node) string {
	n = validationYAMLNode(n)
	if n.Kind == yaml.MappingNode {
		return "[object Object]"
	}
	if n.Kind == yaml.SequenceNode {
		parts := []string{}
		for _, item := range n.Content {
			if validationYAMLType(item) == "null" {
				parts = append(parts, "")
			} else {
				parts = append(parts, validationYAMLKey(item))
			}
		}
		return strings.Join(parts, ",")
	}
	return n.Value
}
func validationYAMLType(n *yaml.Node) string {
	n = validationYAMLNode(n)
	if n == nil {
		return "null"
	}
	switch n.Kind {
	case yaml.MappingNode:
		return "object"
	case yaml.SequenceNode:
		return "array"
	}
	if n.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle|yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
		return "string"
	}
	lower := strings.ToLower(n.Value)
	switch n.Tag {
	case "!!null":
		if lower == "null" || lower == "~" || lower == "" {
			return "null"
		}
	case "!!int", "!!float":
		if regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`).MatchString(n.Value) || regexp.MustCompile(`^[+-]?0(?:x[0-9a-fA-F]+|o[0-7]+)$`).MatchString(n.Value) || lower == ".nan" || lower == ".inf" || lower == "+.inf" || lower == "-.inf" {
			return "number"
		}
	case "!!bool":
		if lower == "true" || lower == "false" {
			return "boolean"
		}
	}
	return "string"
}
func validationYAML(input string) (*yaml.Node, error) {
	// Bun treats an initial U+FEFF as content; yaml.v3 drops a stream BOM.
	// A leading empty line preserves that code point without changing YAML's
	// mappings/scalars or the linter's message-only parse diagnostics.
	if strings.HasPrefix(input, "\ufeff") {
		input = "\n" + input
	}
	decoder := yaml.NewDecoder(strings.NewReader(input))
	docs := []*yaml.Node{}
	for {
		var root yaml.Node
		err := decoder.Decode(&root)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			message := "YAML Parse error: Unexpected token"
			if strings.Contains(err.Error(), "unknown anchor") {
				message = "YAML Parse error: Unresolved alias"
			} else if strings.Contains(err.Error(), "unknown escape") || strings.Contains(err.Error(), "control characters") || strings.Contains(err.Error(), "hexadecimal number") || strings.Contains(err.Error(), "hexdecimal number") {
				message = "YAML Parse error: Unexpected character"
			}
			return nil, errors.New(message)
		}
		if len(root.Content) > 0 {
			docs = append(docs, root.Content[0])
		}
	}
	if len(docs) == 0 {
		return nil, nil
	}
	lines := strings.Split(input, "\n")
	var multilineKey func(*yaml.Node) bool
	multilineKey = func(n *yaml.Node) bool {
		if n.Kind == yaml.AliasNode {
			return false
		}
		if n.Kind == yaml.MappingNode {
			for i := 2; i+1 < len(n.Content); i += 2 {
				key := n.Content[i]
				if key.Line > 0 && key.Line <= len(lines) && strings.HasPrefix(strings.TrimLeft(lines[key.Line-1], " \t"), "? ") {
					return true
				}
			}
		}
		for _, child := range n.Content {
			if multilineKey(child) {
				return true
			}
		}
		return false
	}
	for _, root := range docs {
		if multilineKey(root) {
			return nil, errors.New("YAML Parse error: Multiline implicit key")
		}
	}
	var cycle func(*yaml.Node, map[*yaml.Node]bool) bool
	cycle = func(n *yaml.Node, active map[*yaml.Node]bool) bool {
		if n == nil {
			return false
		}
		if active[n] {
			return true
		}
		active[n] = true
		defer delete(active, n)
		if n.Kind == yaml.AliasNode {
			return cycle(n.Alias, active)
		}
		for _, item := range n.Content {
			if cycle(item, active) {
				return true
			}
		}
		return false
	}
	for _, root := range docs {
		if cycle(root, map[*yaml.Node]bool{}) {
			return nil, errors.New("YAML Parse error: Unresolved alias")
		}
	}
	if len(docs) > 1 {
		return &yaml.Node{Kind: yaml.SequenceNode, Content: docs}, nil
	}
	return docs[0], nil
}
