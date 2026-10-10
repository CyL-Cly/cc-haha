package pluginsapi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/markdownfrontmatter"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

func readMarkdown(path string) (markdownfrontmatter.Parsed, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return markdownfrontmatter.Parsed{}, err
	}
	return markdownfrontmatter.Parse(string(protocol.DecodeJavaScriptUTF8(data)))
}
func describe(body string) json.RawMessage {
	for _, line := range strings.Split(body, "\n") {
		text := trimJS(line)
		if text == "" {
			continue
		}
		heading := strings.TrimLeft(text, "#")
		if heading != text && heading != "" {
			remaining := strings.TrimLeftFunc(heading, javaScriptWhitespace)
			if remaining != heading && remaining != "" && !strings.ContainsAny(remaining, "\u2028\u2029") {
				text = remaining
			}
		}
		units := utf16.Encode([]rune(text))
		if len(units) > 100 {
			units = append(units[:97], '.', '.', '.')
		}
		return protocol.QuoteJavaScriptUTF16(units)
	}
	return raw("No description")
}
func markdownEntries(plugin string, paths []string, stopAtSkill, preferName bool) []protocol.JSONObject {
	result := []protocol.JSONObject{}
	seen := map[string]bool{}
	var scan func(string, []string)
	scan = func(path string, namespace []string) {
		entries, err := os.ReadDir(path)
		if err != nil {
			return
		}
		stop := false
		if stopAtSkill {
			for _, entry := range entries {
				if entry.Type().IsRegular() && strings.EqualFold(entry.Name(), "skill.md") {
					stop = true
				}
			}
		}
		for _, entry := range entries {
			full := filepath.Join(path, entry.Name())
			if entry.IsDir() {
				if !stop {
					scan(full, append(append([]string{}, namespace...), entry.Name()))
				}
				continue
			}
			if !entry.Type().IsRegular() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".md") {
				continue
			}
			parsed, err := readMarkdown(full)
			if err != nil {
				continue
			}
			leaf := entry.Name()[:len(entry.Name())-3]
			if preferName && trimJS(parsed.Frontmatter.String("name")) != "" {
				leaf = trimJS(parsed.Frontmatter.String("name"))
			} else if strings.EqualFold(leaf, "skill") {
				leaf = filepath.Base(filepath.Dir(full))
			}
			parts := append([]string{plugin}, namespace...)
			name := strings.Join(append(parts, leaf), ":")
			description := trimJS(parsed.Frontmatter.String("description"))
			if description == "" {
				description = trimJS(parsed.Frontmatter.String("when-to-use"))
			}
			descriptionJSON := raw(description)
			if description == "" {
				descriptionJSON = describe(parsed.Content)
			}
			if !seen[name] {
				seen[name] = true
				r := rec(field("name", name), protocol.JSONField{Name: "description", Value: descriptionJSON})
				if leaf != name {
					r.Set("displayName", raw(leaf))
				}
				result = append(result, r)
			}
		}
	}
	for _, path := range paths {
		if path != "" {
			scan(path, []string{})
		}
	}
	sortEntries(result)
	return result
}
func readSkill(plugin, path string) *protocol.JSONObject {
	file := filepath.Join(path, "SKILL.md")
	info, err := os.Stat(file)
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}
	parsed, err := readMarkdown(file)
	if err != nil {
		return nil
	}
	base := filepath.Base(path)
	name := trimJS(parsed.Frontmatter.String("name"))
	if name == "" {
		name = base
	}
	description := trimJS(parsed.Frontmatter.String("description"))
	if description == "" {
		for _, line := range strings.Split(parsed.Content, "\n") {
			if trimJS(line) != "" {
				description = trimJS(line)
				break
			}
		}
	}
	if description == "" {
		description = "No description"
	}
	r := rec(field("name", plugin+":"+base), field("description", description), field("pluginName", plugin))
	if name != base {
		r.Set("displayName", raw(name))
	}
	if version := parsed.Get("version"); version != nil && version.Kind != "null" {
		r.Set("version", raw(version.String()))
	}
	return &r
}
func skillEntries(plugin string, paths []string) []protocol.JSONObject {
	out := []protocol.JSONObject{}
	seen := map[string]bool{}
	add := func(value *protocol.JSONObject) {
		if value != nil && !seen[value.String("name")] {
			seen[value.String("name")] = true
			out = append(out, *value)
		}
	}
	for _, path := range paths {
		if path == "" {
			continue
		}
		direct := readSkill(plugin, path)
		if direct != nil && !seen[direct.String("name")] {
			add(direct)
			continue
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				add(readSkill(plugin, filepath.Join(path, entry.Name())))
			}
		}
	}
	sortEntries(out)
	return out
}
func hookEntries(hooks *protocol.JSONObject) []protocol.JSONObject {
	out := []protocol.JSONObject{}
	if hooks == nil {
		return out
	}
	for _, event := range properties(*hooks) {
		for _, v := range arr(hooks.Get(event)) {
			m := obj(v)
			actions := []string{}
			for _, a := range arr(m.Get("hooks")) {
				h := obj(a)
				action := h.String("type")
				switch action {
				case "command":
					action = h.String("command")
				case "prompt", "agent":
					action = h.String("prompt")
				case "http":
					action = h.String("url")
				}
				actions = append(actions, action)
			}
			row := rec(field("event", event), field("actions", actions))
			row.Set("matcher", m.Get("matcher"))
			out = append(out, row)
		}
	}
	c := collate.New(language.English)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].String("event"), out[j].String("event")
		if a != b {
			return c.CompareString(a, b) < 0
		}
		return c.CompareString(out[i].String("matcher"), out[j].String("matcher")) < 0
	})
	return out
}
func mcpEntries(plugin string, servers *protocol.JSONObject, sorted bool) []protocol.JSONObject {
	out := []protocol.JSONObject{}
	if servers == nil {
		return out
	}
	for _, name := range properties(*servers) {
		config := obj(servers.Get(name))
		transport := config.String("type")
		if transport == "" {
			transport = "stdio"
		}
		summary := transport
		if transport == "stdio" {
			parts := []string{}
			if command := config.String("command"); command != "" {
				parts = append(parts, command)
			}
			for _, v := range arr(config.Get("args")) {
				value := stringValue(v)
				if value != "" {
					parts = append(parts, value)
				}
			}
			summary = trimJS(strings.Join(parts, " "))
		} else if config.Get("url") != nil && len(config.Get("url")) > 0 && config.Get("url")[0] == '"' {
			summary = config.String("url")
		}
		out = append(out, rec(field("name", "plugin:"+plugin+":"+name), field("displayName", name), field("transport", transport), field("summary", summary)))
	}
	if sorted {
		sortEntries(out)
	}
	return out
}
