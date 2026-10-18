package pluginregistry

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"

	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginschema"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillregistry"
)

type Options struct {
	HomeDir, ConfigDir, CWD string
	CoworkPlugins           bool
	Files                   FileSystem
	Environment             func(string) string
	Settings                func(context.Context, string) (protocol.JSONObject, error)
	Policy                  func(context.Context) (protocol.JSONObject, error)
	AddDirEnabled           func(context.Context) (protocol.JSONObject, error)
	InlinePaths             []string
	Builtins                *BuiltinRegistry
	CacheSettings           func(context.Context, protocol.JSONObject) error
	ClearSettings           func()
	// Native consumers bind the actual shared state. Callback-only fixtures
	// cannot establish this production settings identity.
	SettingsState *clisettings.State
	Installed     *InstalledRegistry
	// Explicit full refresh owns materialization. Absence is an unimplemented
	// dependency, never an empty successful refresh or a legacy subprocess.
	Prepare    func(context.Context, protocol.JSONObject, string, string, protocol.JSONObject) (Prepared, error)
	ExtractZip func(context.Context, string, string) (string, error)
}
type Loader struct {
	options       Options
	creator       *Creator
	directories   Directories
	mu            sync.Mutex
	cached        *Result
	cachedPending *pluginLoadFuture
	fullPending   *pluginLoadFuture
	cacheID       uint64
}

type pluginLoadFuture struct {
	loader   *Loader
	done     chan struct{}
	result   Result
	err      error
	full     bool
	once     sync.Once
	delegate *pluginLoadFuture
	aliases  []*pluginLoadFuture
}

func (future *pluginLoadFuture) wait(ctx context.Context) (Result, error) {
	value := func() (Result, error) {
		future.loader.mu.Lock()
		defer future.loader.mu.Unlock()
		return snapshotResult(future.result), future.err
	}
	select {
	case <-future.done:
		return value()
	default:
	}
	select {
	case <-future.done:
		return value()
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
}

type CapabilityError struct{ Boundary string }

func (e *CapabilityError) Error() string {
	return "Go plugin capability is not migrated: " + e.Boundary
}
func New(o Options) (*Loader, error) {
	if o.SettingsState != nil {
		if o.CacheSettings != nil || o.ClearSettings != nil {
			return nil, errors.New("native plugin settings require one shared cache writer")
		}
		o.CacheSettings = func(_ context.Context, settings protocol.JSONObject) error {
			if !settings.IsObject() {
				o.SettingsState.ClearPluginSettingsBase()
				return nil
			}
			copy := settings.Clone()
			o.SettingsState.SetPluginSettingsBase(&copy)
			if len(copy.OwnProperties()) > 0 {
				o.SettingsState.ResetSettingsCache()
			}
			return nil
		}
		o.ClearSettings = func() {
			if o.SettingsState.GetPluginSettingsBase() != nil {
				o.SettingsState.ResetSettingsCache()
			}
			o.SettingsState.ClearPluginSettingsBase()
		}
	}
	if !filepath.IsAbs(o.HomeDir) || !filepath.IsAbs(o.ConfigDir) || !filepath.IsAbs(o.CWD) || o.Environment == nil || o.Settings == nil || o.Policy == nil || o.AddDirEnabled == nil || o.Builtins == nil || o.CacheSettings == nil || o.ClearSettings == nil || o.Installed == nil {
		return nil, errors.New("plugin loader requires native settings/policy/builtin/runtime bindings")
	}
	files := o.Files
	if files == nil {
		files = OSFileSystem{}
	}
	creator, e := NewCreator(workingDirectoryFS{files, o.CWD})
	if e != nil {
		return nil, e
	}
	return &Loader{options: o, creator: creator, directories: ResolveDirectories(o.HomeDir, o.ConfigDir, o.CoworkPlugins, o.Environment)}, nil
}

// SharesSettingsState checks immutable construction identity, without reading
// a cache or substituting a ready/absent result for the actual producer.
func (l *Loader) SharesSettingsState(state *clisettings.State) bool {
	return l != nil && state != nil && l.options.SettingsState == state
}
func (l *Loader) Clear() {
	l.mu.Lock()
	l.cached = nil
	l.cachedPending = nil
	l.fullPending = nil
	l.options.ClearSettings()
	l.mu.Unlock()
}
func (l *Loader) LoadCached(ctx context.Context) (Result, error) {
	return l.CachedProducer().Load(ctx)
}
func (l *Loader) LoadFull(ctx context.Context) (Result, error) {
	return l.FullProducer().Load(ctx)
}
func (l *Loader) LoadProject(ctx context.Context, root string) (Result, error) {
	return l.assemble(ctx, root, false)
}
func (l *Loader) EnabledForSkills(ctx context.Context) ([]skillregistry.Plugin, error) {
	l.Clear()
	// Source's collectPluginSkillDirectories explicitly clears installation
	// snapshots before this refresh; generic clearPluginCache does not.
	l.options.Installed.Clear()
	result, e := l.LoadCached(ctx)
	if e != nil {
		return nil, e
	}
	for _, err := range result.Errors {
		if err.String("type") == "plugin-cache-miss" {
			result, e = l.LoadFull(ctx)
			break
		}
	}
	if e != nil {
		return nil, e
	}
	out := []skillregistry.Plugin{}
	for _, p := range result.Enabled {
		out = append(out, skillregistry.Plugin{Name: p.Name, SkillsPath: p.SkillsPath, SkillsPaths: p.SkillsPaths})
	}
	return out, nil
}
func (l *Loader) readObject(path string) protocol.JSONObject {
	input, e := l.creator.readJSON(path)
	if e != nil {
		return protocol.NewJSONObject()
	}
	return object(input)
}
func (l *Loader) known() (protocol.JSONObject, protocol.JSONObject) {
	raw := l.readObject(filepath.Join(l.directories.Primary, "known_marketplaces.json"))
	bytes, _ := raw.MarshalJSON()
	validated, issues := pluginschema.Validate(l.creator.Schemas.Known, bytes)
	if len(issues) > 0 {
		return protocol.NewJSONObject(), raw
	}
	return object(validated), raw
}
func (l *Loader) marketplace(location string) *protocol.JSONObject {
	path := filepath.Join(location, ".claude-plugin", "marketplace.json")
	bytes, e := l.creator.readJSON(path)
	if e != nil {
		if !errors.Is(e, fs.ErrNotExist) && !errors.Is(e, syscall.ENOTDIR) {
			return nil
		}
		bytes, e = l.creator.readJSON(location)
	}
	if e != nil {
		return nil
	}
	parsed, issues := pluginschema.Validate(l.creator.Schemas.Marketplace, bytes)
	if len(issues) > 0 {
		return nil
	}
	result := object(parsed)
	return &result
}
func VersionedPath(config, id, version string) string {
	return VersionedPathIn(filepath.Join(config, "plugins"), id, version)
}

var pluginID = regexp.MustCompile(`(?i)^[a-z0-9][-a-z0-9._]*@[a-z0-9][-a-z0-9._]*$`)

func (l *Loader) assemble(ctx context.Context, project string, full bool) (Result, error) {
	settings, e := l.options.Settings(ctx, project)
	if e != nil {
		return Result{}, e
	}
	policySettings, e := l.options.Policy(ctx)
	if e != nil {
		return Result{}, e
	}
	policy := PolicyFromSettings(policySettings)
	enabled := protocol.NewJSONObject()
	if project == "" {
		additional, e := l.options.AddDirEnabled(ctx)
		if e != nil {
			return Result{}, e
		}
		enabled = additional.Clone()
	}
	for _, property := range object(settings.Get("enabledPlugins")).OwnProperties() {
		_ = enabled.SetPropertyJSON(property.NameJSON, property.Value)
	}
	known, rawKnown := l.known()
	installedData, e := l.options.Installed.Session(ctx)
	if e != nil {
		return Result{}, e
	}
	installed := object(installedData.Get("plugins"))
	catalogs := map[string]*protocol.JSONObject{}
	all := []Loaded{}
	errs := []protocol.JSONObject{}
	for _, property := range enabled.OwnProperties() {
		var id string
		_ = json.Unmarshal(property.NameJSON, &id)
		if !pluginID.MatchString(id) {
			continue
		}
		name, market := nameAndMarket(id)
		if market == "builtin" {
			continue
		}
		isEnabled := IsEnabledSetting(property.Value)
		config := object(known.Get(market))
		active := config.IsObject()
		blocked := active && !policy.Allowed(object(config.Get("source")))
		if (!active && policy.Active()) || blocked {
			byBlock := policy.Blocked(object(config.Get("source")))
			allowed := []string{}
			if !active {
				byBlock = !policy.HasAllowlist
			}
			if !byBlock {
				for _, source := range policy.Allowlist {
					allowed = append(allowed, FormatSource(source))
				}
			}
			errs = append(errs, errorRecord("marketplace-blocked-by-policy", id, fieldValue("plugin", name), fieldValue("marketplace", market), fieldValue("blockedByBlocklist", byBlock), fieldValue("allowedSources", allowed)))
			continue
		}
		catalog, loaded := catalogs[market]
		if !loaded {
			rawConfig := object(rawKnown.Get(market))
			catalog = l.marketplace(rawConfig.String("installLocation"))
			catalogs[market] = catalog
		}
		var entry protocol.JSONObject
		location := config.String("installLocation")
		if !active {
			location = object(rawKnown.Get(market)).String("installLocation")
		}
		if catalog != nil {
			for _, item := range array(catalog.Get("plugins")) {
				p := object(item)
				if p.String("name") == name {
					entry = p
					break
				}
			}
		}
		if !entry.IsObject() {
			if isEnabled {
				errs = append(errs, errorRecord("plugin-not-found", id, fieldValue("pluginId", name), fieldValue("marketplace", market)))
			}
			continue
		}
		installations := array(installed.Get(id))
		var installation protocol.JSONObject
		if project == "" {
			if len(installations) > 0 {
				installation = object(installations[0])
			}
		} else {
			for _, scope := range []string{"local", "project", "user"} {
				for _, item := range installations {
					p := object(item)
					if p.String("scope") == scope && (scope == "user" || filepath.Clean(p.String("projectPath")) == filepath.Clean(project)) {
						installation = p
						break
					}
				}
				if installation.IsObject() {
					break
				}
			}
			if !installation.IsObject() {
				continue
			}
		}
		path := ""
		var loadErr error
		if full {
			var prepared Prepared
			prepared, loadErr = l.options.Prepare(ctx, entry, location, id, installation)
			errs = append(errs, prepared.Errors...)
			if prepared.Skip {
				continue
			}
			path = prepared.Path
		} else if source := entry.Get("source"); jsonKind(source) == "string" {
			stat, e := l.creator.Files.Stat(location)
			if e != nil {
				errs = append(errs, errorRecord("plugin-cache-miss", id, fieldValue("plugin", name), fieldValue("installPath", location)))
				continue
			}
			base := location
			if !stat.IsDir() {
				base = filepath.Dir(location)
			}
			var relative string
			_ = json.Unmarshal(source, &relative)
			path = filepath.Join(base, relative)
		} else {
			path = installation.String("installPath")
			if path == "" || !l.creator.exists(path) {
				value := path
				if value == "" {
					value = "(not recorded)"
				}
				errs = append(errs, errorRecord("plugin-cache-miss", id, fieldValue("plugin", name), fieldValue("installPath", value)))
				continue
			}
		}
		if loadErr != nil {
			errs = append(errs, errorRecord("generic-error", id, fieldValue("plugin", name), fieldValue("error", loadErr.Error())))
			continue
		}
		if strings.HasSuffix(path, ".zip") && envTruthy(l.options.Environment("CLAUDE_CODE_PLUGIN_USE_ZIP_CACHE")) {
			if l.options.ExtractZip == nil {
				return Result{}, &CapabilityError{"native ZIP cache extraction"}
			}
			extracted, err := l.options.ExtractZip(ctx, path, id)
			if err != nil {
				errs = append(errs, errorRecord("plugin-cache-miss", id, fieldValue("plugin", name), fieldValue("installPath", path)))
				continue
			}
			path = extracted
		}
		p, errors, err := l.creator.FinishEntry(ctx, entry, id, isEnabled, path)
		errs = append(errs, errors...)
		if err != nil {
			errs = append(errs, errorRecord("generic-error", id, fieldValue("plugin", name), fieldValue("error", err.Error())))
		} else if p != nil {
			all = append(all, *p)
		}
	}
	if project == "" {
		session := []Loaded{}
		for i, dir := range l.options.InlinePaths {
			path := dir
			if !filepath.IsAbs(path) {
				path = filepath.Join(l.options.CWD, path)
			}
			if !l.creator.exists(path) {
				errs = append(errs, errorRecord("path-not-found", fmtInline(i), fieldValue("path", path), fieldValue("component", "commands")))
				continue
			}
			name := filepath.Base(path)
			p, errors, err := l.creator.Create(ctx, path, name+"@inline", true, name, true)
			if err != nil {
				errs = append(errs, errorRecord("generic-error", fmtInline(i), fieldValue("error", "Failed to load plugin: "+err.Error())))
				continue
			}
			p.Source, p.Repository = p.Name+"@inline", p.Name+"@inline"
			session = append(session, p)
			errs = append(errs, errors...)
		}
		builtin := l.options.Builtins.Load(settings)
		var mergeErrors []protocol.JSONObject
		all, mergeErrors = Merge(session, all, builtin, ManagedNames(policySettings))
		errs = append(errs, mergeErrors...)
	}
	demoted, dependencyErrors := VerifyAndDemote(all)
	errs = append(errs, dependencyErrors...)
	disabled := map[string]bool{}
	for _, id := range demoted {
		disabled[id] = true
	}
	result := Result{Enabled: []Loaded{}, Disabled: []Loaded{}, Errors: errs}
	pluginSettings := protocol.NewJSONObject()
	hasPluginSettings := false
	for _, p := range all {
		// Project capability reads categorize a demoted plugin without mutating
		// its original enabled intent. The session loader applies demotion.
		demoted := disabled[p.Source]
		if demoted && project == "" {
			p.Enabled = false
		}
		if p.Enabled && !demoted {
			result.Enabled = append(result.Enabled, p)
			if p.Settings != nil {
				hasPluginSettings = true
				for _, field := range p.Settings.OwnProperties() {
					_ = pluginSettings.SetPropertyJSON(field.NameJSON, field.Value)
				}
			}
		} else {
			result.Disabled = append(result.Disabled, p)
		}
	}
	if project == "" {
		if !hasPluginSettings {
			pluginSettings = protocol.JSONObject{}
		}
		if e := l.options.CacheSettings(ctx, pluginSettings); e != nil {
			return Result{}, e
		}
	}
	return result, nil
}
func fmtInline(i int) string { return "inline[" + string(raw(i)) + "]" }
func envTruthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "yes", "on":
		return true
	}
	return false
}
