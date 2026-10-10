package memoryapi

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
)

func control(text string) bool {
	for _, r := range text {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}
func validID(id string) bool {
	return id != "" && len(id) <= maxProjectIDBytes && !control(id) && !strings.ContainsAny(id, "/\\") && id != "." && id != ".."
}
func requirePath(value string) (string, error) {
	if value == "" {
		return "", badRequest("Missing memory file path")
	}
	normalized := strings.ReplaceAll(value, "\\", "/")
	invalid := len(normalized) > maxPathBytes || strings.HasPrefix(normalized, "/") || control(normalized) || !strings.HasSuffix(normalized, ".md")
	// Node win32.isAbsolute also recognizes drive roots on POSIX.
	if len(value) >= 3 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) && value[1] == ':' && (value[2] == '/' || value[2] == '\\') {
		invalid = true
	}
	for _, part := range strings.Split(normalized, "/") {
		if part == "" || part == "." || part == ".." || len(part) > maxSegmentBytes {
			invalid = true
		}
	}
	if invalid {
		return "", badRequest("Memory path must be a relative .md file path")
	}
	return normalized, nil
}
func (s *Service) projectsDir() string { return filepath.Join(s.options.ConfigDir, "projects") }
func (s *Service) canonical(cwd string) string {
	start := cwd
	if !filepath.IsAbs(start) {
		start = filepath.Join(s.options.CWD, start)
	}
	root := usercontext.CanonicalGitRoot(usercontext.OSMemoryFiles{}, filepath.EvalSymlinks, start)
	if root == "" {
		return cwd
	}
	return root
}
func (s *Service) projectID(cwd string) string { return projectpath.Sanitize(s.canonical(cwd)) }
func (s *Service) memoryDir(id string, mustExist bool) (string, error) {
	if !validID(id) {
		return "", badRequest("Invalid projectId")
	}
	projects := filepath.Clean(s.projectsDir())
	project := filepath.Join(projects, id)
	if err := assertWithin(project, projects, false); err != nil {
		return "", err
	}
	memory := filepath.Join(project, "memory")
	if directoryExists(project) {
		if err := assertWithin(project, projects, true); err != nil {
			return "", err
		}
	}
	if directoryExists(memory) {
		if err := assertWithin(memory, project, true); err != nil {
			return "", err
		}
	}
	if mustExist && !directoryExists(memory) {
		return "", notFound("Memory project not found: " + id)
	}
	return memory, nil
}
func (s *Service) resolveFile(id, relative string, mustExist bool) (string, error) {
	memory, err := s.memoryDir(id, mustExist)
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(memory, filepath.FromSlash(relative))
	if err = assertWithin(candidate, memory, mustExist); err != nil {
		return "", err
	}
	return candidate, nil
}
func assertWithin(candidate, directory string, mustExist bool) error {
	directory = filepath.Clean(directory)
	candidate = filepath.Clean(candidate)
	if mustExist {
		directory = safeRealpath(directory)
		candidate = safeRealpath(candidate)
	}
	boundary := directory
	if !strings.HasSuffix(boundary, string(filepath.Separator)) {
		boundary += string(filepath.Separator)
	}
	if candidate != directory && !strings.HasPrefix(candidate, boundary) {
		return badRequest("Path escapes memory directory")
	}
	return nil
}
func safeRealpath(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return filepath.Clean(path)
}
func directoryExists(path string) bool { info, err := os.Stat(path); return err == nil && info.IsDir() }
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// ReadDir preserves native enumeration order. Source only sorts explicitly in
// file listing and label search, not project scanning or count traversal.
func readDir(path string) ([]os.DirEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.ReadDir(-1)
}
