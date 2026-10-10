package workflowcommands

import (
	"context"
	"path/filepath"
	"sort"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

// Paths are produced by the actual cache-only plugin registry. A single-file
// manifest entry intentionally loads all workflows in its parent directory.
type PluginDirectory struct{ Name, Path string }

func (s *Service) BeginDefinitions(scope *cooperative.Scope, cwd *string) (*ownerasync.OwnerFuture[[]Definition], error) {
	base, e := s.beginBaseDefinitions(scope, cwd)
	if e != nil || s.options.PluginDirectories == nil {
		return base, e
	}
	out, _ := ownerasync.NewOwnerFuture[[]Definition](s.options.Owner)
	paths := ioFuture(s, scope, func() ([]PluginDirectory, error) { return s.options.PluginDirectories(context.Background()) })
	_, e = paths.Then(scope, func(next *cooperative.Scope, dirs []PluginDirectory, cause error) error {
		if cause != nil {
			return out.Reject(next, cause)
		}
		results := make([][]Definition, len(dirs))
		remaining := len(dirs)
		finish := func(current *cooperative.Scope) error {
			_, e := base.Then(current, func(scope *cooperative.Scope, definitions []Definition, cause error) error {
				if cause != nil {
					return out.Reject(scope, cause)
				}
				byName := map[string]Definition{}
				for _, group := range results {
					for _, definition := range group {
						byName[definition.Object.String("name")] = definition
					}
				}
				for _, definition := range definitions {
					byName[definition.Object.String("name")] = definition
				}
				names := []string{}
				for name := range byName {
					names = append(names, name)
				}
				sort.SliceStable(names, func(i, j int) bool { return s.collator.CompareString(names[i], names[j]) < 0 })
				values := make([]Definition, 0, len(names))
				for _, name := range names {
					values = append(values, byName[name])
				}
				return out.Resolve(scope, values)
			})
			return e
		}
		if remaining == 0 {
			return finish(next)
		}
		for i, dir := range dirs {
			index, dir := i, dir
			stat := ioFuture(s, next, func() (bool, error) {
				info, e := s.options.FS.Stat(dir.Path)
				if e != nil {
					return false, e
				}
				return info.IsDir(), nil
			})
			_, e := stat.Then(next, func(current *cooperative.Scope, isDir bool, cause error) error {
				complete := func(scope *cooperative.Scope, definitions []Definition, cause error) error {
					if cause != nil {
						return out.Reject(scope, cause)
					}
					for _, definition := range definitions {
						definition.Object.Set("name", quote(dir.Name+":"+definition.Object.String("name")))
						results[index] = append(results[index], definition)
					}
					remaining--
					if remaining == 0 {
						return finish(scope)
					}
					return nil
				}
				if cause != nil {
					return complete(current, nil, nil)
				}
				path := dir.Path
				if !isDir {
					path = filepath.Dir(path)
				}
				future := s.beginDirectory(current, path, "plugin")
				_, e := future.Then(current, complete)
				return e
			})
			if e != nil {
				return out.Reject(next, e)
			}
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}
