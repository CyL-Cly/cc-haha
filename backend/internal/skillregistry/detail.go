package skillregistry

import (
	"encoding/json"
	"math"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/markdownfrontmatter"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

type TreeNode struct {
	Name     string     `json:"name"`
	Path     string     `json:"path"`
	Type     string     `json:"type"`
	Children []TreeNode `json:"children,omitempty"`
}
type File struct {
	Path        string               `json:"path"`
	Content     json.RawMessage      `json:"content"`
	Language    string               `json:"language"`
	Frontmatter *protocol.JSONObject `json:"frontmatter,omitempty"`
	Body        json.RawMessage      `json:"body,omitempty"`
	IsEntry     bool                 `json:"isEntry"`
}
type Detail struct {
	Meta       Meta                 `json:"meta"`
	Tree       []TreeNode           `json:"tree"`
	Files      []File               `json:"files"`
	SkillRoot  string               `json:"skillRoot"`
	MarketMeta *protocol.JSONObject `json:"marketMeta,omitempty"`
}

var languages = map[string]string{"md": "markdown", "ts": "typescript", "tsx": "typescript", "js": "javascript", "jsx": "javascript", "json": "json", "yaml": "yaml", "yml": "yaml", "sh": "bash", "bash": "bash", "py": "python", "toml": "toml", "css": "css", "html": "html", "txt": "text", "xml": "xml", "sql": "sql", "rs": "rust", "go": "go"}

func languageFor(name string) string {
	parts := strings.Split(name, ".")
	ext := strings.ToLower(parts[len(parts)-1])
	if value := languages[ext]; value != "" {
		return value
	}
	return "text"
}
func (s *Service) fileTree(root string) ([]TreeNode, []File) {
	files := []File{}
	count := 0
	comparer := collate.New(language.English)
	var walk func(string) []TreeNode
	walk = func(dir string) []TreeNode {
		nodes := []TreeNode{}
		if count >= 50 {
			return nodes
		}
		entries, err := s.options.FS.ReadDir(dir)
		if err != nil {
			return nodes
		}
		sort.SliceStable(entries, func(i, j int) bool {
			if entries[i].IsDir() != entries[j].IsDir() {
				return entries[i].IsDir()
			}
			return comparer.CompareString(entries[i].Name(), entries[j].Name()) < 0
		})
		for _, entry := range entries {
			if count >= 50 {
				break
			}
			name := entry.Name()
			if strings.HasPrefix(name, ".") || name == "node_modules" || name == "__pycache__" {
				continue
			}
			path := filepath.Join(dir, name)
			relative, _ := filepath.Rel(root, path)
			if entry.IsDir() {
				node := TreeNode{Name: name, Path: relative, Type: "directory"}
				node.Children = walk(path)
				nodes = append(nodes, node)
			} else if entry.Type().IsRegular() {
				nodes = append(nodes, TreeNode{Name: name, Path: relative, Type: "file"})
				stat, err := s.options.FS.Stat(path)
				if err != nil || stat.Size() > 100*1024 {
					continue
				}
				bytes, err := s.options.FS.ReadFile(path)
				if err != nil {
					continue
				}
				text := readText(bytes)
				language := languageFor(name)
				item := File{Path: relative, Language: language, IsEntry: relative == "SKILL.md"}
				item.Content, _ = json.Marshal(text)
				if item.IsEntry && language == "markdown" {
					parsed, err := markdownfrontmatter.Parse(text)
					if err != nil {
						continue
					}
					item.Content, item.Body = parsed.ContentJSON, parsed.ContentJSON
					item.Frontmatter = &parsed.Frontmatter
				}
				files = append(files, item)
				count++
			}
		}
		return nodes
	}
	return walk(root), files
}

var marketDir = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

func (s *Service) marketMeta(name string) *protocol.JSONObject {
	bytes, err := s.options.FS.ReadFile(filepath.Join(s.options.ConfigDir, "skills", name, ".market-meta.json"))
	if err != nil {
		return nil
	}
	var object protocol.JSONObject
	if object.UnmarshalJSON(bytes) != nil || !object.IsObject() {
		return nil
	}
	source, slug := object.String("source"), object.String("slug")
	directory := strings.ToLower(slug)
	if source != "clawhub" && source != "skillhub" || !marketDir.MatchString(directory) || strings.Contains(directory, "..") || directory != name || object.String("id") != source+":"+slug || object.String("installedAt") == "" {
		return nil
	}
	var number float64
	count := object.Get("fileCount")
	if len(count) == 0 || string(count) == "null" || json.Unmarshal(count, &number) != nil || number < 0 || math.Trunc(number) != number {
		return nil
	}
	result := protocol.NewJSONObject()
	for _, key := range []string{"id", "source", "slug", "installedAt", "fileCount"} {
		result.Set(key, object.Get(key))
	}
	for _, key := range []string{"version", "signatureVerified"} {
		raw := object.Get(key)
		if len(raw) == 0 {
			continue
		}
		var value any
		if json.Unmarshal(raw, &value) != nil {
			return nil
		}
		if key == "version" {
			if _, ok := value.(string); !ok {
				return nil
			}
		} else {
			if _, ok := value.(bool); !ok {
				return nil
			}
		}
		result.Set(key, raw)
	}
	return &result
}
