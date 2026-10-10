package memoryapi

import (
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/markdownfrontmatter"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
	"golang.org/x/text/unicode/norm"
)

type Project struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	MemoryDir string `json:"memoryDir"`
	Exists    bool   `json:"exists"`
	FileCount int    `json:"fileCount"`
	IsCurrent bool   `json:"isCurrent"`
}
type File struct {
	Path        string  `json:"path"`
	Name        string  `json:"name"`
	Bytes       int64   `json:"bytes"`
	UpdatedAt   string  `json:"updatedAt"`
	Type        *string `json:"type,omitempty"`
	Description *string `json:"description,omitempty"`
	Title       string  `json:"title"`
	IsIndex     bool    `json:"isIndex"`
}

func unsanitizeLabel(id string) string {
	if strings.HasPrefix(id, "-") {
		id = "/" + id[1:]
	}
	return strings.ReplaceAll(id, "-", "/")
}
func (s *Service) Projects(cwd string) ([]Project, error) {
	if cwd == "" {
		cwd = s.options.CWD
	}
	current := s.projectID(cwd)
	projects := []Project{}
	seen := map[string]int{}
	add := func(id string, isCurrent bool) {
		if !validID(id) {
			return
		}
		if index, exists := seen[id]; exists {
			projects[index].IsCurrent = projects[index].IsCurrent || isCurrent
			return
		}
		seen[id] = len(projects)
		projects = append(projects, Project{ID: id, Label: unsanitizeLabel(id), MemoryDir: filepath.Join(s.projectsDir(), id, "memory"), IsCurrent: isCurrent})
	}
	add(current, true)
	entries, _ := readDir(s.projectsDir())
	for _, entry := range entries {
		if entry.IsDir() {
			add(entry.Name(), entry.Name() == current)
		}
	}
	resolved := []Project{}
	for _, project := range projects {
		project.FileCount = countMarkdown(project.MemoryDir)
		label, err := s.projectLabel(project.ID, cwd)
		if err != nil {
			return nil, err
		}
		project.Label = label
		project.Exists = project.FileCount > 0 || directoryExists(project.MemoryDir)
		if project.Exists {
			resolved = append(resolved, project)
		}
	}
	comparer := collate.New(language.English)
	sort.SliceStable(resolved, func(i, j int) bool {
		a, b := resolved[i], resolved[j]
		if a.IsCurrent != b.IsCurrent {
			return a.IsCurrent
		}
		if a.FileCount != b.FileCount {
			return a.FileCount > b.FileCount
		}
		return comparer.CompareString(a.Label, b.Label) < 0
	})
	return resolved, nil
}
func (s *Service) Files(id string) ([]File, error) {
	memory, err := s.memoryDir(id, false)
	if err != nil {
		return nil, err
	}
	files := []File{}
	if !directoryExists(memory) {
		return files, nil
	}
	comparer := collate.New(language.English)
	var walk func(string, string) error
	walk = func(dir, prefix string) error {
		if len(files) >= MaxFiles {
			return nil
		}
		entries, err := readDir(dir)
		if err != nil {
			return nil
		}
		sort.SliceStable(entries, func(i, j int) bool {
			a, b := entries[i], entries[j]
			if a.IsDir() != b.IsDir() {
				return a.IsDir()
			}
			return comparer.CompareString(a.Name(), b.Name()) < 0
		})
		for _, entry := range entries {
			if len(files) >= MaxFiles {
				break
			}
			if strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules" {
				continue
			}
			relative := entry.Name()
			if prefix != "" {
				relative = prefix + "/" + relative
			}
			full := filepath.Join(dir, entry.Name())
			if entry.IsDir() {
				if err = walk(full, relative); err != nil {
					return err
				}
				continue
			}
			if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			stat, err := os.Stat(full)
			if err != nil {
				return err
			}
			item := File{Path: relative, Name: entry.Name(), Bytes: stat.Size(), UpdatedAt: iso(stat.ModTime()), Title: strings.TrimSuffix(entry.Name(), ".md"), IsIndex: relative == "MEMORY.md"}
			if item.IsIndex {
				item.Title = "MEMORY.md"
			}
			if stat.Size() <= MaxFileBytes {
				raw, e := os.ReadFile(full)
				if e == nil {
					parsed, e := markdownfrontmatter.Parse(string(protocol.DecodeJavaScriptUTF8(raw)))
					if e == nil {
						if value := parsed.Get("type"); value != nil && value.Kind == "string" {
							text := value.String()
							switch text {
							case "user", "feedback", "project", "reference":
								item.Type = &text
							}
						}
						if value := parsed.Get("description"); value != nil && value.Kind == "string" {
							text := value.String()
							item.Description = &text
						}
					}
				}
			}
			files = append(files, item)
		}
		return nil
	}
	if err = walk(memory, ""); err != nil {
		return nil, err
	}
	sort.SliceStable(files, func(i, j int) bool {
		a, b := files[i], files[j]
		if a.IsIndex != b.IsIndex {
			return a.IsIndex
		}
		return comparer.CompareString(a.Path, b.Path) < 0
	})
	return files, nil
}
func countMarkdown(dir string) int {
	count := 0
	var walk func(string)
	walk = func(current string) {
		if count >= MaxFiles {
			return
		}
		entries, err := readDir(current)
		if err != nil {
			return
		}
		for _, entry := range entries {
			if count >= MaxFiles {
				break
			}
			if strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			full := filepath.Join(current, entry.Name())
			if entry.IsDir() {
				walk(full)
			} else if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".md") {
				count++
			}
		}
	}
	walk(dir)
	return count
}
func (s *Service) projectLabel(id, cwd string) (string, error) {
	root := s.canonical(cwd)
	if projectpath.Sanitize(root) == id {
		return root, nil
	}
	label, err := s.sessionLabel(id)
	if err != nil {
		return "", err
	}
	if label != "" {
		return label, nil
	}
	if label = s.filesystemLabel(id); label != "" {
		return label, nil
	}
	return unsanitizeLabel(id), nil
}
func (s *Service) sessionLabel(id string) (string, error) {
	dir := filepath.Join(s.projectsDir(), id)
	entries, err := readDir(dir)
	if err != nil {
		return "", nil
	}
	type session struct {
		path     string
		modified int64
	}
	sessions := []session{}
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if stat, e := os.Stat(path); e == nil {
			sessions = append(sessions, session{path, stat.ModTime().UnixNano()})
		}
	}
	sort.SliceStable(sessions, func(i, j int) bool { return sessions[i].modified > sessions[j].modified })
	if len(sessions) > 10 {
		sessions = sessions[:10]
	}
	for _, file := range sessions {
		f, e := os.Open(file.path)
		if e != nil {
			return "", e
		}
		raw, e := io.ReadAll(io.LimitReader(f, 64*1024))
		_ = f.Close()
		if e != nil {
			raw = nil
		}
		head := string(protocol.DecodeJavaScriptUTF8(raw))
		candidate, exists := extractString(head, "cwd")
		if !exists {
			candidate, exists = extractString(head, "workDir")
		}
		if !exists {
			candidate, _ = extractString(head, "projectPath")
		}
		if candidate != "" && filepath.IsAbs(candidate) {
			return norm.NFC.String(candidate), nil
		}
	}
	return "", nil
}
func extractString(text, key string) (string, bool) {
	for _, pattern := range []string{"\"" + key + "\":\"", "\"" + key + "\": \""} {
		index := strings.Index(text, pattern)
		if index < 0 {
			continue
		}
		start := index + len(pattern)
		for i := start; i < len(text); i++ {
			if text[i] == '\\' {
				i++
				continue
			}
			if text[i] == '"' {
				raw := text[start:i]
				if strings.Contains(raw, "\\") {
					if value, ok := rawString([]byte("\"" + raw + "\"")); ok {
						return value, true
					}
				}
				return raw, true
			}
		}
	}
	return "", false
}
func prefixMatches(id, prefix string) bool {
	if id == prefix {
		return true
	}
	if strings.HasSuffix(prefix, "-") {
		return strings.HasPrefix(id, prefix)
	}
	return strings.HasPrefix(id, prefix+"-")
}
func (s *Service) filesystemLabel(id string) string {
	roots := []string{s.options.Home, s.options.Environment("HOME"), s.options.Environment("USERPROFILE"), "/private/tmp", "/tmp"}
	seen := map[string]bool{}
	comparer := collate.New(language.English)
	for _, root := range roots {
		if root == "" || !filepath.IsAbs(root) || seen[root] {
			continue
		}
		seen[root] = true
		root = filepath.Clean(root)
		if !prefixMatches(id, projectpath.Sanitize(root)) {
			continue
		}
		visited := 0
		var search func(string, int) string
		search = func(candidate string, depth int) string {
			if visited >= 2000 {
				return ""
			}
			visited++
			candidateID := projectpath.Sanitize(candidate)
			if candidateID == id {
				return candidate
			}
			if depth >= 24 || !prefixMatches(id, candidateID) {
				return ""
			}
			entries, err := readDir(candidate)
			if err != nil {
				return ""
			}
			sort.SliceStable(entries, func(i, j int) bool { return comparer.CompareString(entries[i].Name(), entries[j].Name()) < 0 })
			for _, entry := range entries {
				if !entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
					continue
				}
				child := filepath.Join(candidate, entry.Name())
				if !prefixMatches(id, projectpath.Sanitize(child)) {
					continue
				}
				if entry.Type()&os.ModeSymlink != 0 && !directoryExists(child) {
					continue
				}
				if found := search(child, depth+1); found != "" {
					return found
				}
			}
			return ""
		}
		if found := search(root, 0); found != "" {
			return norm.NFC.String(found)
		}
	}
	return ""
}
