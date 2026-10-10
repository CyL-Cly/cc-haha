package worktree

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/gitbranch"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

var validSHA = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

func trim(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		return r == '\ufeff' || r == '\t' || r == '\n' || r == '\v' || r == '\f' || r == '\r' || r == ' ' || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000
	})
}
func (s *Service) read(path string) string {
	b, e := s.options.Files.ReadFile(path)
	if e != nil {
		return ""
	}
	return string(protocol.DecodeJavaScriptUTF8(b))
}
func (s *Service) GitRoot(start string) string {
	current, e := filepath.Abs(start)
	if e != nil {
		return ""
	}
	for {
		st, e := s.options.Files.Stat(filepath.Join(current, ".git"))
		if e == nil && (st.IsDir() || st.Mode().IsRegular()) {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}
func (s *Service) GitDir(root string) string {
	path := filepath.Join(root, ".git")
	st, e := s.options.Files.Stat(path)
	if e != nil {
		return ""
	}
	if st.Mode().IsRegular() {
		content := trim(s.read(path))
		if strings.HasPrefix(content, "gitdir:") {
			path = trim(strings.TrimPrefix(content, "gitdir:"))
			if !filepath.IsAbs(path) {
				path = filepath.Join(root, path)
			}
			return filepath.Clean(path)
		}
	}
	return path
}
func (s *Service) CommonDir(dir string) string {
	value := trim(s.read(filepath.Join(dir, "commondir")))
	if value == "" {
		return dir
	}
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return filepath.Join(dir, value)
}
func (s *Service) ResolveRef(dir, ref string) string {
	value := s.resolveRefInDir(dir, ref, 0)
	if value != "" {
		return value
	}
	common := s.CommonDir(dir)
	if common != dir {
		return s.resolveRefInDir(common, ref, 0)
	}
	return ""
}
func (s *Service) resolveRefInDir(dir, ref string, depth int) string {
	if depth > 16 || !gitbranch.SafeRef(ref) {
		return ""
	}
	b, e := s.options.Files.ReadFile(filepath.Join(dir, filepath.FromSlash(ref)))
	if e == nil {
		value := trim(string(protocol.DecodeJavaScriptUTF8(b)))
		if strings.HasPrefix(value, "ref:") {
			target := trim(strings.TrimPrefix(value, "ref:"))
			if !gitbranch.SafeRef(target) {
				return ""
			}
			return s.resolveRefInDir(dir, target, depth+1)
		}
		if validSHA.MatchString(value) {
			return value
		}
		return ""
	}
	for _, line := range strings.Split(s.read(filepath.Join(dir, "packed-refs")), "\n") {
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "^") {
			continue
		}
		index := strings.IndexByte(line, ' ')
		if index >= 0 && line[index+1:] == ref {
			sha := line[:index]
			if validSHA.MatchString(sha) {
				return sha
			}
			return ""
		}
	}
	return ""
}
func (s *Service) WorktreeHead(path string) string {
	// Original fast resume reads exactly this .git file without an upward walk.
	pointer := trim(s.read(filepath.Join(path, ".git")))
	if !strings.HasPrefix(pointer, "gitdir:") {
		return ""
	}
	dir := trim(strings.TrimPrefix(pointer, "gitdir:"))
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(path, dir)
	}
	head := trim(s.read(filepath.Join(dir, "HEAD")))
	if strings.HasPrefix(head, "ref:") {
		return s.ResolveRef(dir, trim(strings.TrimPrefix(head, "ref:")))
	}
	if validSHA.MatchString(head) {
		return head
	}
	return ""
}
func (s *Service) DefaultBranch(root string) string {
	dir := s.GitDir(root)
	if dir == "" {
		return "main"
	}
	common := s.CommonDir(dir)
	value := trim(s.read(filepath.Join(common, "refs/remotes/origin/HEAD")))
	if strings.HasPrefix(value, "ref:") {
		ref := trim(strings.TrimPrefix(value, "ref:"))
		if strings.HasPrefix(ref, "refs/remotes/origin/") {
			branch := strings.TrimPrefix(ref, "refs/remotes/origin/")
			if gitbranch.SafeRef(branch) {
				return branch
			}
		}
	}
	for _, name := range []string{"main", "master"} {
		if s.ResolveRef(common, "refs/remotes/origin/"+name) != "" {
			return name
		}
	}
	return "main"
}
