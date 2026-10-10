package gitbranch

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/diagnostics"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/text/unicode/norm"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

func trim(v string) string {
	return strings.TrimFunc(v, func(r rune) bool {
		return r == '\ufeff' || r == '\u0009' || r == '\u000b' || r == '\u000c' || r == '\u0020' || r == '\u00a0' || r == '\u1680' || r >= '\u2000' && r <= '\u200a' || r == '\u202f' || r == '\u205f' || r == '\u3000' || r == '\n' || r == '\r' || r == '\u2028' || r == '\u2029'
	})
}
func decoded(v []byte) string { return string(protocol.DecodeJavaScriptUTF8(v)) }
func nullable(v string) json.RawMessage {
	if v == "" {
		return raw(nil)
	}
	return raw(v)
}
func stringValue(r json.RawMessage) string { var v string; _ = json.Unmarshal(r, &v); return v }
func (s *Service) FindRoot(scope *cooperative.Scope, start string) (string, error) {
	if e := s.check(scope); e != nil {
		return "", e
	}
	if !utf8.ValidString(start) || strings.ContainsRune(start, 0) {
		return "", &CapabilityError{"original path UTF8/NUL"}
	}
	for i, e := range s.roots {
		if e.start == start {
			s.roots = append(append(s.roots[:i:i], s.roots[i+1:]...), e)
			return e.root, nil
		}
	}
	started := s.options.Now() // Original start Date.now is read on every miss.
	if s.options.Diagnostics != nil {
		if err := s.options.Diagnostics.Log("info", "find_git_root_started", nil); err != nil {
			return "", err
		}
	}
	current, e := filepath.Abs(start)
	if e != nil {
		return "", e
	}
	count := 0
	completed := func(found bool) error {
		duration := float64(s.options.Now()) - float64(started)
		if s.options.Diagnostics == nil {
			return nil
		}
		present := json.RawMessage("false")
		if found {
			present = json.RawMessage("true")
		}
		data := protocol.NewJSONObject(
			protocol.JSONField{Name: "duration_ms", Value: diagnostics.Number(duration)},
			protocol.JSONField{Name: "stat_count", Value: diagnostics.Number(float64(count))},
			protocol.JSONField{Name: "found", Value: present},
		)
		return s.options.Diagnostics.Log("info", "find_git_root_completed", &data)
	}
	found := ""
	for {
		count++
		info, statErr := s.options.StatSync(filepath.Join(current, ".git"))
		if statErr == nil && (info.IsDir() || info.Mode().IsRegular()) {
			// Source's found log is inside the original stat try/catch. A date
			// or serialization failure there falls through to the next ancestor.
			if completed(true) == nil {
				found = norm.NFC.String(current)
				break
			}
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	if found == "" {
		// The not-found log is outside that catch and must propagate failure.
		if err := completed(false); err != nil {
			return "", err
		}
	}
	s.roots = append(s.roots, rootEntry{start, found})
	if len(s.roots) > 512 {
		s.roots = s.roots[1:]
	}
	return found, nil
}
func (s *Service) Resolve(scope *cooperative.Scope, start string) (*Future, error) {
	if e := s.check(scope); e != nil {
		return nil, e
	}
	if !utf8.ValidString(start) || strings.ContainsRune(start, 0) {
		return nil, &CapabilityError{"original resolve path UTF8/NUL"}
	}
	cwd, e := filepath.Abs(start)
	if e != nil {
		return nil, e
	}
	f := s.future()
	if v, ok := s.dirs[cwd]; ok {
		s.settle(scope, f, result{raw: nullable(v.path)})
		return f, nil
	}
	root, e := s.FindRoot(scope, cwd)
	if e != nil {
		return nil, e
	}
	if root == "" {
		s.dirs[cwd] = dirValue{}
		s.settle(scope, f, result{raw: raw(nil)})
		return f, nil
	}
	path := filepath.Join(root, ".git")
	finish := func(scope *cooperative.Scope, v string) {
		s.dirs[cwd] = dirValue{v}
		s.settle(scope, f, result{raw: nullable(v)})
	}
	s.io(f, path, true, func(scope *cooperative.Scope, p physical) {
		if p.err != nil {
			finish(scope, "")
			return
		}
		if !p.info.Mode().IsRegular() {
			finish(scope, path)
			return
		}
		s.io(f, path, false, func(scope *cooperative.Scope, p physical) {
			if p.err != nil {
				finish(scope, "")
				return
			}
			content := trim(decoded(p.bytes))
			if strings.HasPrefix(content, "gitdir:") {
				d := trim(content[len("gitdir:"):])
				if filepath.IsAbs(d) {
					finish(scope, filepath.Clean(d))
				} else {
					finish(scope, filepath.Join(root, d))
				}
				return
			}
			finish(scope, path)
		})
	})
	return f, nil
}
func SafeRef(v string) bool {
	if v == "" || strings.HasPrefix(v, "-") || strings.HasPrefix(v, "/") || strings.Contains(v, "..") {
		return false
	}
	for _, p := range strings.Split(v, "/") {
		if p == "." || p == "" {
			return false
		}
	}
	for _, r := range v {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/._+@-", r)) {
			return false
		}
	}
	return true
}
func validSHA(v string) bool {
	if len(v) != 40 && len(v) != 64 {
		return false
	}
	for _, r := range v {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
func (s *Service) ReadHead(scope *cooperative.Scope, dir string) (*Future, error) {
	if e := s.check(scope); e != nil {
		return nil, e
	}
	f := s.future()
	s.readHead(scope, f, dir)
	return f, nil
}
func (s *Service) readHead(scope *cooperative.Scope, f *Future, dir string) {
	s.io(f, filepath.Join(dir, "HEAD"), false, func(scope *cooperative.Scope, p physical) {
		if p.err != nil {
			s.settle(scope, f, result{raw: raw(nil)})
			return
		}
		content := trim(decoded(p.bytes))
		if strings.HasPrefix(content, "ref:") {
			ref := trim(content[4:])
			if strings.HasPrefix(ref, "refs/heads/") {
				name := ref[len("refs/heads/"):]
				if !SafeRef(name) {
					s.settle(scope, f, result{raw: raw(nil)})
					return
				}
				s.settle(scope, f, result{raw: raw(struct {
					Type string `json:"type"`
					Name string `json:"name"`
				}{"branch", name})})
				return
			}
			if !SafeRef(ref) {
				s.settle(scope, f, result{raw: raw(nil)})
				return
			}
			s.ref(scope, f, dir, ref, map[string]bool{}, func(scope *cooperative.Scope, sha string, e error) {
				if e != nil {
					s.settle(scope, f, result{err: e})
					return
				}
				s.settle(scope, f, result{raw: raw(struct {
					Type string `json:"type"`
					SHA  string `json:"sha"`
				}{"detached", sha})})
			})
			return
		}
		if !validSHA(content) {
			s.settle(scope, f, result{raw: raw(nil)})
			return
		}
		s.settle(scope, f, result{raw: raw(struct {
			Type string `json:"type"`
			SHA  string `json:"sha"`
		}{"detached", content})})
	})
}
func (s *Service) common(scope *cooperative.Scope, f *Future, dir string, done func(*cooperative.Scope, string)) {
	s.io(f, filepath.Join(dir, "commondir"), false, func(scope *cooperative.Scope, p physical) {
		if p.err != nil {
			done(scope, "")
			return
		}
		v := trim(decoded(p.bytes))
		if filepath.IsAbs(v) {
			done(scope, filepath.Clean(v))
		} else {
			done(scope, filepath.Join(dir, v))
		}
	})
}
func (s *Service) ref(scope *cooperative.Scope, f *Future, dir, name string, seen map[string]bool, done func(*cooperative.Scope, string, error)) {
	key := dir + "\x00" + name
	if seen[key] {
		done(scope, "", &CapabilityError{"cyclic symref: Source has no finite termination"})
		return
	}
	next := make(map[string]bool, len(seen)+1)
	for k, v := range seen {
		next[k] = v
	}
	next[key] = true
	s.refIn(scope, f, dir, name, next, func(scope *cooperative.Scope, value string, e error) {
		if e != nil || value != "" {
			done(scope, value, e)
			return
		}
		s.common(scope, f, dir, func(scope *cooperative.Scope, common string) {
			if common == "" || common == dir {
				done(scope, "", nil)
				return
			}
			s.refIn(scope, f, common, name, next, done)
		})
	})
}
func (s *Service) refIn(scope *cooperative.Scope, f *Future, dir, name string, seen map[string]bool, done func(*cooperative.Scope, string, error)) {
	s.io(f, filepath.Join(dir, name), false, func(scope *cooperative.Scope, p physical) {
		if p.err == nil {
			content := trim(decoded(p.bytes))
			if strings.HasPrefix(content, "ref:") {
				target := trim(content[4:])
				if !SafeRef(target) {
					done(scope, "", nil)
					return
				}
				s.ref(scope, f, dir, target, seen, done)
				return
			}
			if validSHA(content) {
				done(scope, content, nil)
			} else {
				done(scope, "", nil)
			}
			return
		}
		s.io(f, filepath.Join(dir, "packed-refs"), false, func(scope *cooperative.Scope, p physical) {
			if p.err == nil {
				for _, line := range strings.Split(decoded(p.bytes), "\n") {
					if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "^") {
						continue
					}
					i := strings.IndexByte(line, ' ')
					if i < 0 {
						continue
					}
					if line[i+1:] == name {
						sha := line[:i]
						if !validSHA(sha) {
							sha = ""
						}
						done(scope, sha, nil)
						return
					}
				}
			}
			done(scope, "", nil)
		})
	})
}
