package resumestate

import (
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf16"
)

func (s *Service) beginOwnedPlan(scope *cooperative.Scope, log *OriginalLog, sid json.RawMessage) (*ownerasync.OwnerFuture[bool], error) {
	out, e := ownerasync.NewOwnerFuture[bool](s.owned.state.Owner())
	if e != nil {
		return nil, e
	}
	var slug json.RawMessage
	for _, message := range log.History.Messages {
		candidate := message.Get("slug")
		if truthy(candidate) {
			slug = candidate
			break
		}
	}
	if !truthy(slug) {
		resolveOwned(out, scope, false, nil)
		return out, nil
	}
	key, keyOK := originalUTF8String(sid)
	slugName, slugOK := originalUTF8String(slug)
	if !keyOK || !slugOK || strings.ContainsAny(slugName, "/\\\x00") {
		resolveOwned(out, scope, false, &CapabilityError{Capability: "plan slug original UTF8 leaf"})
		return out, nil
	}
	cache, e := s.owned.state.PlanSlugCache(scope)
	if e != nil {
		resolveOwned(out, scope, false, e)
		return out, nil
	}
	if e = cache.Set(scope, key, slugName); e != nil {
		resolveOwned(out, scope, false, e)
		return out, nil
	}
	directory, e := s.ownedPlansDir()
	if e != nil {
		resolveOwned(out, scope, false, e)
		return out, nil
	}
	path := filepath.Join(directory, slugName+".md")
	read, e := s.nativeRead(scope, path)
	if e != nil {
		resolveOwned(out, scope, false, e)
		return out, nil
	}
	_, e = read.Then(scope, func(next *cooperative.Scope, _ []byte, failure error) error {
		if failure == nil {
			resolveOwned(out, next, true, nil)
			return nil
		}
		if !errors.Is(failure, fs.ErrNotExist) {
			s.options.OnError(failure)
			resolveOwned(out, next, false, nil)
			return nil
		}
		if s.options.EnvironmentKind == "byoc" || s.options.EnvironmentKind == "anthropic_cloud" {
			resolveOwned(out, next, false, &CapabilityError{Capability: "remote plan recovery native write continuation"})
			return nil
		}
		resolveOwned(out, next, false, nil)
		return nil
	})
	if e != nil {
		resolveOwned(out, scope, false, e)
	}
	return out, nil
}

// Source's plans directory getter is memoized and mkdirSync: it stays on Owner.
// Settings are sampled here through the genuine shared initial-settings service.
func (s *Service) ownedPlansDir() (string, error) {
	if s.plansDirectory != "" {
		return s.plansDirectory, nil
	}
	settings, e := s.owned.startup.GetInitialSettings()
	if e != nil {
		return "", e
	}
	path := filepath.Join(s.options.ConfigDir, "plans")
	if configured := settings.Get("plansDirectory"); truthy(configured) {
		if _, ok := originalUTF8String(configured); !ok {
			return "", &CapabilityError{Capability: "plansDirectory original string"}
		}
		resolved := text(configured)
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(s.owned.state.Snapshot().CWD, resolved)
		}
		resolved = filepath.Clean(resolved)
		root := s.owned.state.Snapshot().CWD
		if resolved != root && !strings.HasPrefix(resolved, root+string(filepath.Separator)) {
			s.options.OnError(failure("plansDirectory must be within project root: %s", text(configured)))
		} else {
			path = resolved
		}
	}
	if e = s.options.Files.MkdirAll(path); e != nil {
		s.options.OnError(e)
	}
	s.plansDirectory = path
	return path, nil
}

// Public Go filesystem/cache strings cannot carry unpaired code units. Reject
// that unported leaf instead of silently replacing it before path or cache work.
func originalUTF8String(raw json.RawMessage) (string, bool) {
	value, ok := units(raw)
	if !ok {
		return "", false
	}
	text := string(utf16.Decode(value))
	return text, slices.Equal(value, utf16.Encode([]rune(text)))
}
