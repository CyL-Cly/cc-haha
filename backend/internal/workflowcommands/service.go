package workflowcommands

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/gitbranch"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
	"golang.org/x/text/unicode/norm"
)

//go:embed bundled_deep_research.js
var bundledScript string

type FileSystem interface {
	ReadDir(string) ([]Entry, error)
	Stat(string) (fs.FileInfo, error)
	ReadFile(string) ([]byte, error)
}
type Entry struct {
	Name               string
	File, SymbolicLink bool
}
type OSFileSystem struct{}

func (OSFileSystem) ReadDir(path string) ([]Entry, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	entries, e := f.ReadDir(-1)
	if e != nil {
		return nil, e
	}
	out := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, Entry{entry.Name(), entry.Type().IsRegular(), entry.Type()&fs.ModeSymlink != 0})
	}
	return out, nil
}
func (OSFileSystem) Stat(path string) (fs.FileInfo, error) { return os.Stat(path) }
func (OSFileSystem) ReadFile(path string) ([]byte, error)  { return os.ReadFile(path) }

type Options struct {
	Owner                 *cooperative.Owner
	State                 *bootstrapstate.State
	Config                *bootstrapstate.ConfigResolver
	Gate                  *Gate
	Git                   *gitbranch.Service
	Home                  string
	FS                    FileSystem
	OriginalInlinePlugins []string
	Diagnostic            func(*cooperative.Scope, string)
	PluginDirectories     func(context.Context) ([]PluginDirectory, error)
	CanonicalGitRoot      func(*cooperative.Scope, string) (string, error)
}
type Service struct {
	options  Options
	bundled  []Definition
	commands map[string]*ownerasync.OwnerFuture[[]*Command]
	collator *collate.Collator
	metadata *Metadata
}

func New(scope *cooperative.Scope, options Options) (*Service, error) {
	if !scope.BelongsTo(options.Owner) || options.Owner == nil || options.State == nil || !options.State.Owns(options.Owner) || options.Config == nil || options.Gate == nil || options.Gate.owner != options.Owner || options.Git == nil || !filepath.IsAbs(options.Home) || options.Diagnostic == nil {
		return nil, cooperative.ErrRestricted
	}
	if len(options.OriginalInlinePlugins) > 0 {
		return nil, &CapabilityError{"plugin loader side effects not migrated; namespaced Source absence preserved, not a fake cache"}
	}
	if options.FS == nil {
		options.FS = OSFileSystem{}
	}
	metadata, e := NewMetadata(scope, options.Owner)
	if e != nil {
		return nil, e
	}
	return &Service{metadata: metadata, options: options, commands: map[string]*ownerasync.OwnerFuture[[]*Command]{}, collator: collate.New(language.English)}, nil
}
func (s *Service) check(scope *cooperative.Scope) error {
	if s == nil || !scope.BelongsTo(s.options.Owner) {
		return cooperative.ErrRestricted
	}
	return nil
}
func (s *Service) ClearCommandCache(scope *cooperative.Scope) error {
	if e := s.check(scope); e != nil {
		return e
	}
	s.commands = map[string]*ownerasync.OwnerFuture[[]*Command]{}
	return nil
}
func (s *Service) ResetBundledForTesting(scope *cooperative.Scope) error {
	if e := s.check(scope); e != nil {
		return e
	}
	s.bundled = nil
	return nil
}
func (s *Service) bundledDefinitions(scope *cooperative.Scope) ([]Definition, error) {
	if s.bundled != nil {
		return s.bundled, nil
	}
	parsed, e := s.metadata.ParseScript(scope, bundledScript)
	if e != nil {
		return nil, e
	}
	if parsed.Error != "" {
		return nil, errors.New("Bundled workflow failed to parse: " + parsed.Error)
	}
	definition := definitionFrom("built-in", parsed.Meta, bundledScript, "")
	s.bundled = []Definition{definition}
	return s.bundled, nil
}
func definitionFrom(source string, meta protocol.JSONObject, script, path string) Definition {
	object := protocol.NewJSONObject(protocol.JSONField{Name: "source", Value: quote(source)}, protocol.JSONField{Name: "name", Value: meta.Get("name")}, protocol.JSONField{Name: "description", Value: meta.Get("description")}, protocol.JSONField{Name: "whenToUse", Value: meta.Get("whenToUse")}, protocol.JSONField{Name: "phases", Value: meta.Get("phases")}, protocol.JSONField{Name: "script", Value: quote(script)})
	if path != "" {
		object.Set("filePath", quote(path))
	}
	return Definition{object}
}

// BeginCommands is the additive Workflow source cache used by the outer cwd-keyed
// registry. It retains rejected/disabled results until explicit invalidation.
func (s *Service) BeginCommands(scope *cooperative.Scope, cwd string) (*ownerasync.OwnerFuture[[]*Command], error) {
	if e := s.check(scope); e != nil {
		return nil, e
	}
	if f, ok := s.commands[cwd]; ok {
		return f, nil
	}
	f, e := s.BeginFreshCommands(scope, &cwd)
	if e != nil {
		return nil, e
	}
	s.commands[cwd] = f
	return f, nil
}
func (s *Service) BeginFreshCommands(scope *cooperative.Scope, cwd *string) (*ownerasync.OwnerFuture[[]*Command], error) {
	if e := s.check(scope); e != nil {
		return nil, e
	}
	out, _ := ownerasync.NewOwnerFuture[[]*Command](s.options.Owner)
	reason, e := s.options.Gate.DisabledReason(scope)
	if e != nil {
		_ = out.Reject(scope, e)
		return out, nil
	}
	if reason != "" {
		_ = out.Resolve(scope, []*Command{})
		return out, nil
	}
	definitions, e := s.BeginDefinitions(scope, cwd)
	if e != nil {
		_ = out.Reject(scope, e)
		return out, nil
	}
	_, e = definitions.Then(scope, func(next *cooperative.Scope, values []Definition, err error) error {
		if err != nil {
			return out.Reject(next, err)
		}
		commands := make([]*Command, 0, len(values))
		for _, value := range values {
			command, err := NewCommand(next, s.options.Gate, value)
			if err != nil {
				return out.Reject(next, err)
			}
			commands = append(commands, command)
		}
		return out.Resolve(next, commands)
	})
	if e != nil {
		_ = out.Reject(scope, e)
	}
	return out, nil
}
func (s *Service) beginBaseDefinitions(scope *cooperative.Scope, cwd *string) (*ownerasync.OwnerFuture[[]Definition], error) {
	if e := s.check(scope); e != nil {
		return nil, e
	}
	out, _ := ownerasync.NewOwnerFuture[[]Definition](s.options.Owner)
	originalCWD := s.options.State.Snapshot().OriginalCWD
	if cwd != nil {
		originalCWD = *cwd
	}
	dirs, e := s.projectDirectories(scope, originalCWD)
	if e != nil {
		var cap *CapabilityError
		if errors.As(e, &cap) {
			_ = out.Reject(scope, e)
			return out, nil
		}
		s.options.Diagnostic(scope, "loadWorkflows: project-dir walk failed: "+e.Error())
		dirs = []string{}
	}
	config, e := s.options.Config.ConfigHome(scope)
	if e != nil {
		_ = out.Reject(scope, e)
		return out, nil
	}
	paths := append([]string{filepath.Join(config, "workflows")}, dirs...)
	results := make([][]Definition, len(paths))
	left := len(paths)
	for i, path := range paths {
		source := "projectSettings"
		if i == 0 {
			source = "userSettings"
		}
		index := i
		dir := s.beginDirectory(scope, path, source)
		_, e = dir.Then(scope, func(next *cooperative.Scope, definitions []Definition, err error) error {
			if err != nil {
				return out.Reject(next, err)
			}
			results[index] = definitions
			left--
			if left != 0 {
				return nil
			}
			bundled, err := s.bundledDefinitions(next)
			if err != nil {
				return out.Reject(next, err)
			}
			byName := map[string]Definition{}
			insert := func(definitions []Definition) {
				for _, definition := range definitions {
					var name string
					_ = jsonString(definition.Object.Get("name"), &name)
					byName[name] = definition
				}
			}
			insert(bundled)
			insert(results[0])
			for j := len(results) - 1; j > 0; j-- {
				insert(results[j])
			}
			names := make([]string, 0, len(byName))
			for name := range byName {
				names = append(names, name)
			}
			sort.SliceStable(names, func(i, j int) bool { return s.collator.CompareString(names[i], names[j]) < 0 })
			values := make([]Definition, 0, len(names))
			for _, name := range names {
				values = append(values, byName[name])
			}
			return out.Resolve(next, values)
		})
		if e != nil {
			_ = out.Reject(scope, e)
		}
	}
	return out, nil
}
func (s *Service) beginDirectory(scope *cooperative.Scope, path, source string) *ownerasync.OwnerFuture[[]Definition] {
	out, _ := ownerasync.NewOwnerFuture[[]Definition](s.options.Owner)
	read := ioFuture(s, scope, func() ([]Entry, error) { return s.options.FS.ReadDir(path) })
	_, _ = read.Then(scope, func(next *cooperative.Scope, entries []Entry, err error) error {
		if err != nil {
			return out.Resolve(next, []Definition{})
		}
		values := make([]*Definition, len(entries))
		remaining := len(entries)
		if remaining == 0 {
			return out.Resolve(next, []Definition{})
		}
		complete := func(scope *cooperative.Scope, index int, value *Definition) error {
			values[index] = value
			remaining--
			if remaining != 0 {
				return nil
			}
			definitions := []Definition{}
			for _, value := range values {
				if value != nil {
					definitions = append(definitions, *value)
				}
			}
			return out.Resolve(scope, definitions)
		}
		for index, entry := range entries {
			i := index
			if (!entry.File && !entry.SymbolicLink) || !strings.HasSuffix(entry.Name, ".js") {
				_ = complete(next, i, nil)
				continue
			}
			filePath := filepath.Join(path, entry.Name)
			stat := ioFuture(s, next, func() (fs.FileInfo, error) { return s.options.FS.Stat(filePath) })
			_, _ = stat.Then(next, func(afterStat *cooperative.Scope, info fs.FileInfo, err error) error {
				if err != nil {
					s.options.Diagnostic(afterStat, "Workflow "+filePath+" could not be read: "+err.Error())
					return complete(afterStat, i, nil)
				}
				if info.Size() > MaxScriptBytes {
					s.options.Diagnostic(afterStat, fmtSize(filePath))
					return complete(afterStat, i, nil)
				}
				read := ioFuture(s, afterStat, func() ([]byte, error) { return s.options.FS.ReadFile(filePath) })
				_, _ = read.Then(afterStat, func(afterRead *cooperative.Scope, data []byte, err error) error {
					if err != nil {
						s.options.Diagnostic(afterRead, "Workflow "+filePath+" could not be read: "+err.Error())
						return complete(afterRead, i, nil)
					}
					script := string(protocol.DecodeJavaScriptUTF8(data))
					parsed, err := s.metadata.ParseScript(afterRead, script)
					if err != nil {
						return out.Reject(afterRead, err)
					}
					if parsed.Error != "" {
						s.options.Diagnostic(afterRead, "Workflow "+filePath+" has invalid meta: "+parsed.Error+" — skipping")
						return complete(afterRead, i, nil)
					}
					value := definitionFrom(source, parsed.Meta, script, filePath)
					return complete(afterRead, i, &value)
				})
				return nil
			})
		}
		return nil
	})
	return out
}
func ioFuture[T any](s *Service, scope *cooperative.Scope, work func() (T, error)) *ownerasync.OwnerFuture[T] {
	future, _ := ownerasync.NewOwnerFuture[T](s.options.Owner)
	go func() {
		value, err := work()
		_, postErr := s.options.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(next *cooperative.Scope) error {
			if err != nil {
				return future.Reject(next, err)
			}
			return future.Resolve(next, value)
		})
		if postErr != nil {
			future.RejectAdministrative(postErr)
		}
	}()
	return future
}
func fmtSize(path string) string { return "Workflow " + path + " exceeds 524288 bytes — skipping" }
func (s *Service) projectDirectories(scope *cooperative.Scope, cwd string) ([]string, error) {
	cwdRoot, e := s.options.Git.FindRoot(scope, cwd)
	if e != nil {
		return nil, e
	}
	sessionRoot, e := s.options.Git.FindRoot(scope, s.options.State.Snapshot().ProjectRoot)
	if e != nil {
		return nil, e
	}
	boundary := cwdRoot
	if cwdRoot != "" && sessionRoot != "" {
		info, e := s.options.FS.Stat(filepath.Join(cwdRoot, ".git"))
		if e == nil && info.Mode().IsRegular() && s.options.CanonicalGitRoot == nil {
			return nil, &CapabilityError{"worktree canonical repository security validation"}
		}
		nCWD := norm.NFC.String(cwdRoot)
		nSession := norm.NFC.String(sessionRoot)
		canonical := cwdRoot
		if s.options.CanonicalGitRoot != nil {
			canonical, e = s.options.CanonicalGitRoot(scope, cwd)
			if e != nil {
				return nil, e
			}
		}
		if norm.NFC.String(canonical) != nSession && nCWD != nSession && strings.HasPrefix(nCWD, nSession+string(filepath.Separator)) {
			boundary = sessionRoot
		}
	}
	current, e := filepath.Abs(cwd)
	if e != nil {
		return nil, e
	}
	home := norm.NFC.String(filepath.Clean(s.options.Home))
	dirs := []string{}
	for norm.NFC.String(current) != home {
		path := filepath.Join(current, ".claude", "workflows")
		_, statErr := s.options.FS.Stat(path)
		if statErr == nil {
			dirs = append(dirs, path)
		} else if !errors.Is(statErr, fs.ErrNotExist) && !errors.Is(statErr, fs.ErrPermission) {
			return nil, statErr
		}
		if boundary != "" && norm.NFC.String(current) == norm.NFC.String(boundary) {
			break
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return dirs, nil
}

func jsonString(raw []byte, out *string) error { return json.Unmarshal(raw, out) }
