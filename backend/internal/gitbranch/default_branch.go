package gitbranch

import (
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

// DefaultBranch shares the original watcher, directory resolver and ref reader.
// It reads origin's loose HEAD, then main/master loose or packed refs, without
// invoking git or inventing a branch from the current checkout.
func (s *Service) DefaultBranch(scope *cooperative.Scope) (*BranchFuture, error) {
	if err := s.check(scope); err != nil {
		return nil, err
	}
	f := s.future()
	s.then(scope, s.ensure(scope), func(next *cooperative.Scope, started result) {
		if started.err != nil {
			s.settle(next, f, started)
			return
		}
		if s.watchFault != nil {
			s.settle(next, f, result{err: s.watchFault})
			return
		}
		if s.defaultBranch != nil && !s.defaultBranch.dirty {
			s.settle(next, f, result{raw: s.defaultBranch.value})
			return
		}
		if s.defaultBranch != nil {
			s.defaultBranch.dirty = false
		}
		resolved, err := s.Resolve(next, s.options.CWD())
		if err != nil {
			s.settle(next, f, result{err: err})
			return
		}
		s.then(next, resolved, func(next *cooperative.Scope, resolved result) {
			if resolved.err != nil {
				s.settle(next, f, resolved)
				return
			}
			finish := func(next *cooperative.Scope, branch string) {
				if s.defaultBranch == nil {
					s.defaultBranch = &cacheEntry{value: raw(branch)}
				} else if !s.defaultBranch.dirty {
					s.defaultBranch.value = raw(branch)
				}
				s.settle(next, f, result{raw: raw(branch)})
			}
			dir := stringValue(resolved.raw)
			if dir == "" {
				finish(next, "main")
				return
			}
			s.common(next, f, dir, func(next *cooperative.Scope, common string) {
				if common == "" {
					common = dir
				}
				s.io(f, filepath.Join(common, "refs/remotes/origin/HEAD"), false, func(next *cooperative.Scope, p physical) {
					if p.err == nil {
						text := trim(decoded(p.bytes))
						if strings.HasPrefix(text, "ref:") {
							target := trim(text[4:])
							if strings.HasPrefix(target, "refs/remotes/origin/") {
								branch := target[len("refs/remotes/origin/"):]
								if SafeRef(branch) {
									finish(next, branch)
									return
								}
							}
						}
					}
					s.ref(next, f, common, "refs/remotes/origin/main", map[string]bool{}, func(next *cooperative.Scope, sha string, err error) {
						if err != nil {
							s.settle(next, f, result{err: err})
							return
						}
						if sha != "" {
							finish(next, "main")
							return
						}
						s.ref(next, f, common, "refs/remotes/origin/master", map[string]bool{}, func(next *cooperative.Scope, sha string, err error) {
							if err != nil {
								s.settle(next, f, result{err: err})
								return
							}
							if sha != "" {
								finish(next, "master")
								return
							}
							finish(next, "main")
						})
					})
				})
			})
		})
	})
	return &BranchFuture{f}, nil
}
