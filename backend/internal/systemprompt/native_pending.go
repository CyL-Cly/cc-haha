package systemprompt

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
	"io/fs"
)

// NativePromptPrimitives expose original physical operations, never rendered
// sections or settings. Implementations dispatch each OS call separately and
// resume the same exclusive Owner token before parsing/cache changes.
type NativePromptPrimitives interface {
	usercontext.NativeMemoryPrimitives
	Lstat(string) (fs.FileInfo, error)
	MkdirAll(string) error
	IsGit(string) (bool, error)
	OSInfo() (OSInfo, error)
}

func nativePromptValue[T any](scope *cooperative.Scope, owner *cooperative.Owner, lanes *cooperative.NativePromptContinuations, ctx context.Context, produce func(*cooperative.Scope) (T, error)) (*ownerasync.OwnerFuture[T], error) {
	future, err := ownerasync.NewOwnerFuture[T](owner)
	if err != nil {
		return nil, err
	}
	var value T
	err = lanes.Start(scope, ctx, func(next *cooperative.Scope) error {
		var err error
		value, err = produce(next)
		if err == nil {
			err = context.Cause(ctx)
		}
		return err
	}, func(next *cooperative.Scope, failure error) {
		if failure != nil {
			_ = future.Reject(next, failure)
		} else {
			_ = future.Resolve(next, value)
		}
	})
	if err != nil {
		_ = future.RejectAdministrative(err)
	}
	return future, err
}

// BeginResolveOnOwner begins the real default-prompt body through its first
// pending await. The original service/options and section cache remain shared.
// Settings are read only after skills/style/environment actually completed.
func (s *Service) BeginResolveOnOwner(scope *cooperative.Scope, owner *cooperative.Owner, lanes *cooperative.NativePromptContinuations, lifetime context.Context, tools []protocol.JSONObject, model string, styles *StyleLoader, primitives func(*cooperative.Scope) NativePromptPrimitives) (*ownerasync.OwnerFuture[[]json.RawMessage], error) {
	if s == nil || !scope.BelongsTo(owner) || !lanes.Owns(owner) || styles == nil || primitives == nil {
		return nil, cooperative.ErrRestricted
	}
	if err := lanes.CheckCaller(scope); err != nil {
		return nil, err
	}
	s.mu.Lock()
	o := s.options
	o.AdditionalDirectories = append([]string{}, o.AdditionalDirectories...)
	s.mu.Unlock()
	o.Model = model
	return nativePromptValue(scope, owner, lanes, lifetime, func(next *cooperative.Scope) ([]json.RawMessage, error) {
		if envEnabled(o.Environment, "CLAUDE_CODE_SIMPLE") {
			return s.resolveForModel(lifetime, tools, model)
		}
		skills, err := nativePromptValue(next, owner, lanes, lifetime, func(*cooperative.Scope) ([]Command, error) { return o.SkillCommands(lifetime, o.CWD()) })
		if err != nil {
			return nil, err
		}
		style, err := nativePromptValue(next, owner, lanes, lifetime, func(part *cooperative.Scope) (*OutputStyle, error) {
			files := primitives(part)
			return styles.ResolveWithNativeFiles(lifetime, o.CWD(), files, files.ReadDir, files.Realpath, files.Lstat, files.WaitMemory)
		})
		if err != nil {
			return nil, err
		}
		environment, err := s.beginNativeEnvironment(next, owner, lanes, lifetime, o, primitives)
		if err != nil {
			return nil, err
		}
		// Register all three independent outcomes before awaiting the aggregate.
		ready, err := ownerasync.NewOwnerFuture[struct{}](owner)
		if err != nil {
			return nil, err
		}
		var skillValues []Command
		var styleValue *OutputStyle
		remaining := 3
		settle := func(part *cooperative.Scope, failure error) error {
			if failure != nil {
				return ready.Reject(part, failure)
			}
			remaining--
			if remaining == 0 {
				return ready.Resolve(part, struct{}{})
			}
			return nil
		}
		if _, err = skills.Then(next, func(part *cooperative.Scope, value []Command, failure error) error {
			skillValues = value
			return settle(part, failure)
		}); err != nil {
			return nil, err
		}
		if _, err = style.Then(next, func(part *cooperative.Scope, value *OutputStyle, failure error) error {
			styleValue = value
			return settle(part, failure)
		}); err != nil {
			return nil, err
		}
		if _, err = environment.Then(next, func(part *cooperative.Scope, _ string, failure error) error { return settle(part, failure) }); err != nil {
			return nil, err
		}
		if _, err = ready.Await(next); err != nil {
			return nil, err
		}
		settings, err := o.InitialSettings()
		if err != nil {
			return nil, err
		}
		return s.renderWithOptions(lifetime, o, tools, skillValues, styleValue, settings, func(sections []section) ([]*string, error) {
			return s.resolveNativeSections(next, owner, lanes, lifetime, sections, func(part *cooperative.Scope, item section) (*string, error) {
				files := primitives(part)
				switch item.name {
				case "memory":
					if o.AutoMemory != nil {
						memory := *o.AutoMemory
						memory.Directory, memory.Mkdir = files.MemoryDirectory, files.MkdirAll
						return memory.Load(lifetime, o.Environment, settings)
					}
				case "env_info_simple":
					environment, err := s.beginNativeEnvironment(part, owner, lanes, lifetime, o, primitives)
					if err != nil {
						return nil, err
					}
					text, err := environment.Await(part)
					if err != nil {
						return nil, err
					}
					return pointer(text), nil
				}
				return item.compute()
			})
		})
	})
}

func (s *Service) resolveNativeSections(scope *cooperative.Scope, owner *cooperative.Owner, lanes *cooperative.NativePromptContinuations, lifetime context.Context, sections []section, compute func(*cooperative.Scope, section) (*string, error)) ([]*string, error) {
	s.mu.Lock()
	captured := s.cache
	s.mu.Unlock()
	values := make([]*string, len(sections))
	all, err := ownerasync.NewOwnerFuture[[]*string](owner)
	if err != nil {
		return nil, err
	}
	remaining := len(sections)
	if remaining == 0 {
		_ = all.Resolve(scope, values)
		return all.Await(scope)
	}
	for i, entry := range sections {
		index, item := i, entry
		future, err := nativePromptValue(scope, owner, lanes, lifetime, func(part *cooperative.Scope) (*string, error) {
			s.mu.Lock()
			value, exists := captured[item.name]
			s.mu.Unlock()
			if !item.volatile && exists {
				return value, nil
			}
			value, err := compute(part, item)
			if err == nil {
				err = context.Cause(lifetime)
			}
			if err == nil {
				s.mu.Lock()
				s.cache[item.name] = value
				s.mu.Unlock()
			}
			return value, err
		})
		if err != nil {
			return nil, err
		}
		if _, err = future.Then(scope, func(next *cooperative.Scope, value *string, failure error) error {
			if failure != nil {
				return all.Reject(next, failure)
			}
			values[index] = value
			remaining--
			if remaining == 0 {
				return all.Resolve(next, values)
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	return all.Await(scope)
}

func (s *Service) beginNativeEnvironment(scope *cooperative.Scope, owner *cooperative.Owner, lanes *cooperative.NativePromptContinuations, lifetime context.Context, o Options, primitives func(*cooperative.Scope) NativePromptPrimitives) (*ownerasync.OwnerFuture[string], error) {
	return nativePromptValue(scope, owner, lanes, lifetime, func(part *cooperative.Scope) (string, error) {
		git, err := nativePromptValue(part, owner, lanes, lifetime, func(work *cooperative.Scope) (bool, error) { return primitives(work).IsGit(o.CWD()) })
		if err != nil {
			return "", err
		}
		osinfo, err := nativePromptValue(part, owner, lanes, lifetime, func(work *cooperative.Scope) (OSInfo, error) { return primitives(work).OSInfo() })
		if err != nil {
			return "", err
		}
		ready, err := ownerasync.NewOwnerFuture[struct{}](owner)
		if err != nil {
			return "", err
		}
		remaining := 2
		var isGit bool
		var info OSInfo
		settle := func(next *cooperative.Scope, failure error) error {
			if failure != nil {
				return ready.Reject(next, failure)
			}
			remaining--
			if remaining == 0 {
				return ready.Resolve(next, struct{}{})
			}
			return nil
		}
		if _, err = git.Then(part, func(next *cooperative.Scope, value bool, failure error) error {
			isGit = value
			return settle(next, failure)
		}); err != nil {
			return "", err
		}
		if _, err = osinfo.Then(part, func(next *cooperative.Scope, value OSInfo, failure error) error {
			info = value
			return settle(next, failure)
		}); err != nil {
			return "", err
		}
		if _, err = ready.Await(part); err != nil {
			return "", err
		}
		return s.environmentValues(o, isGit, info), nil
	})
}
