// Package opentargets ports src/server/services/openTargetService.ts and its
// HTTP adapter. Native OS queries are primitives; discovery, ranking, caches,
// path validation, icon resolution and launch plans are Go business logic.
package opentargets

import (
	"context"
	"encoding/base64"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const DefaultTTL = 30000
const MaxNativeApplications = 5
const maxCachedPaths = 64
const maxRegisteredApplications = 256

type Target struct {
	ID        string  `json:"id"`
	Kind      string  `json:"kind"`
	Label     string  `json:"label"`
	Icon      string  `json:"icon"`
	IconURL   string  `json:"iconUrl,omitempty"`
	Platform  string  `json:"platform"`
	AppPath   string  `json:"appPath,omitempty"`
	BundleID  *string `json:"bundleId,omitempty"`
	IsDefault *bool   `json:"isDefault,omitempty"`
}

// Native application targets always serialize bundleId, including null.
func (t Target) MarshalJSON() ([]byte, error) { return marshalTarget(t) }

type List struct {
	Platform        string   `json:"platform"`
	Targets         []Target `json:"targets"`
	PrimaryTargetID *string  `json:"primaryTargetId"`
	CachedAt        int64    `json:"cachedAt"`
	TTLMillis       int64    `json:"ttlMs"`
}
type Application struct {
	AppPath       string   `json:"appPath"`
	BundleID      *string  `json:"bundleId"`
	DisplayName   string   `json:"displayName"`
	IsDefault     bool     `json:"isDefault"`
	UseCount      *float64 `json:"useCount"`
	CanonicalPath *string  `json:"canonicalPath,omitempty"`
}
type ApplicationList struct {
	DefaultApplicationPath *string       `json:"defaultApplicationPath"`
	Applications           []Application `json:"applications"`
}
type LaunchResult struct {
	Code   int    `json:"code"`
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
}
type Icon struct {
	ContentType string
	Data        []byte
}
type OpenResult struct {
	OK       bool   `json:"ok"`
	TargetID string `json:"targetId"`
	Path     string `json:"path"`
}
type Error struct {
	Status        int
	Code, Message string
}

func (e *Error) Error() string                        { return e.Message }
func apiError(status int, message, code string) error { return &Error{status, code, message} }

// Primitives describe OS capabilities only. Tests can isolate external GUI
// launches/LaunchServices without replacing any service or HTTP business.
type Primitives struct {
	ResolveCommand          func(context.Context, string) (string, error)
	CommandExists           func(context.Context, string) bool
	PathExists              func(string) bool
	Launch                  func(context.Context, string, []string) (LaunchResult, error)
	ReadDirNames            func(string) []string
	ReadTextFile            func(string) *string
	ReadPlistValue          func(context.Context, string, string) *string
	ConvertIconToPNG        func(context.Context, string, int) ([]byte, error)
	ListApplicationsForFile func(context.Context, string) (ApplicationList, error)
}
type Options struct {
	Home, ConfigDir, CWD, Platform, TempDir string
	Environment                             map[string]string
	Now                                     func() int64
	TTLMillis                               *int64
	Primitives                              Primitives
}
type Service struct {
	options       Options
	primitives    Primitives
	definitions   []definition
	mu            sync.Mutex
	cache         *List
	icons         map[string]Icon
	paths         map[string]applicationCache
	pathOrder     []string
	registry      map[string]Application
	registryOrder []string
}
type applicationCache struct {
	applications []Application
	cachedAt     int64
}
type resolvedPath struct {
	path                  string
	directory, executable bool
}
type launchPlan struct {
	command string
	args    []string
}

func New(o Options) (*Service, error) {
	if !filepath.IsAbs(o.Home) || !filepath.IsAbs(o.ConfigDir) || !filepath.IsAbs(o.CWD) || o.Platform == "" || o.Environment == nil {
		return nil, errors.New("native Open Targets requires explicit home, config directory, cwd, platform and environment")
	}
	if o.Now == nil {
		o.Now = func() int64 { return time.Now().UnixMilli() }
	}
	if o.TTLMillis == nil {
		ttl := int64(DefaultTTL)
		o.TTLMillis = &ttl
	}
	if o.TempDir == "" {
		o.TempDir = o.Environment["TMPDIR"]
		if o.TempDir == "" {
			o.TempDir = o.Environment["TEMP"]
		}
		if o.TempDir == "" {
			o.TempDir = os.TempDir()
		}
	}
	if !filepath.IsAbs(o.TempDir) {
		return nil, errors.New("native Open Targets requires absolute temporary directory")
	}
	s := &Service{options: o, definitions: definitions(o.Home), icons: map[string]Icon{}, paths: map[string]applicationCache{}, registry: map[string]Application{}}
	s.primitives = s.bindPrimitives(o.Primitives)
	return s, nil
}
func (s *Service) List(ctx context.Context, forceRefresh bool) (List, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.list(ctx, forceRefresh)
}
func (s *Service) list(ctx context.Context, force bool) (List, error) {
	if !force && s.cache != nil && s.options.Now()-s.cache.CachedAt < *s.options.TTLMillis {
		return *s.cache, nil
	}
	targets := []Target{}
	for _, d := range s.definitions {
		if s.detected(ctx, d) {
			targets = append(targets, s.buildTarget(d))
		}
	}
	result := List{Platform: s.options.Platform, Targets: targets, CachedAt: s.options.Now(), TTLMillis: *s.options.TTLMillis}
	if len(targets) > 0 {
		id := targets[0].ID
		result.PrimaryTargetID = &id
	}
	s.cache = &result
	return result, nil
}
func (s *Service) ListForPath(ctx context.Context, path string) (List, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	target, err := s.validatePath(path)
	if err != nil {
		return List{}, err
	}
	global, err := s.list(ctx, false)
	if err != nil {
		return List{}, err
	}
	apps, err := s.applicationsForPath(ctx, target)
	if err != nil {
		return List{}, err
	}
	targets := []Target{}
	var primary *string
	for _, a := range apps {
		item := s.buildApplication(a)
		targets = append(targets, item)
		if primary == nil && a.IsDefault {
			id := item.ID
			primary = &id
		}
	}
	if s.canSystemOpen(ctx) {
		item := s.buildSystem()
		targets = append(targets, item)
		if primary == nil {
			id := item.ID
			primary = &id
		}
	}
	for _, kind := range []string{"ide", "file_manager"} {
		for _, item := range global.Targets {
			if item.Kind == kind {
				targets = append(targets, item)
			}
		}
	}
	if primary == nil && len(targets) > 0 {
		id := targets[0].ID
		primary = &id
	}
	return List{s.options.Platform, targets, primary, s.options.Now(), *s.options.TTLMillis}, nil
}
func (s *Service) Open(ctx context.Context, id, path string) (OpenResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, exists := s.definition(id)
	system := id == "system-default"
	application := strings.HasPrefix(id, "application:")
	if !exists && !system && !application {
		return OpenResult{}, apiError(400, "Unknown open target: "+id, "OPEN_TARGET_UNKNOWN")
	}
	target, err := s.validatePath(path)
	if err != nil {
		return OpenResult{}, err
	}
	var plan *launchPlan
	available := false
	if system {
		if !s.canSystemOpen(ctx) {
			return OpenResult{}, apiError(400, "System-default opening is unavailable", "OPEN_TARGET_UNAVAILABLE")
		}
		if blockedExtension(target.path) || !target.directory && target.executable {
			return OpenResult{}, apiError(400, "System-default opening is blocked for executable paths: "+target.path, "OPEN_TARGET_PATH_EXECUTABLE")
		}
		available = true
		switch s.options.Platform {
		case "darwin":
			plan = &launchPlan{"open", []string{target.path}}
		case "win32":
			plan = &launchPlan{"explorer.exe", []string{target.path}}
		case "linux":
			plan = &launchPlan{"xdg-open", []string{target.path}}
		}
	} else if application {
		apps, e := s.applicationsForPath(ctx, target)
		if e != nil {
			return OpenResult{}, e
		}
		for _, a := range apps {
			if applicationID(a) == id {
				available = true
				plan = &launchPlan{"open", []string{"-a", a.AppPath, target.path}}
				break
			}
		}
	} else {
		list, e := s.list(ctx, false)
		if e != nil {
			return OpenResult{}, e
		}
		for _, item := range list.Targets {
			if item.ID == id {
				available = true
				plan = s.launchPlan(ctx, d, target)
				break
			}
		}
	}
	if !available {
		return OpenResult{}, apiError(400, "Open target is not available on "+s.options.Platform+": "+id, "OPEN_TARGET_UNAVAILABLE")
	}
	if plan == nil {
		return OpenResult{}, apiError(400, "Unable to launch open target: "+id, "OPEN_TARGET_UNAVAILABLE")
	}
	result, err := s.primitives.Launch(ctx, plan.command, plan.args)
	if err != nil {
		return OpenResult{}, err
	}
	if result.Code != 0 {
		return OpenResult{}, apiError(500, "Failed to launch open target: "+id, "OPEN_TARGET_LAUNCH_FAILED")
	}
	return OpenResult{true, id, target.path}, nil
}
func (s *Service) GetIcon(ctx context.Context, id string, size float64) (Icon, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	normalized := 64
	if !math.IsNaN(size) && !math.IsInf(size, 0) {
		normalized = int(math.Min(256, math.Max(16, math.Floor(size+0.5))))
	}
	key := iconKey(s.options.Platform, id, normalized)
	if icon, exists := s.icons[key]; exists {
		return icon, nil
	}
	var path string
	if strings.HasPrefix(id, "application:") {
		if app, exists := s.registry[id]; exists {
			path = s.bundleIcon(ctx, app.AppPath, []string{app.DisplayName})
		}
		if path == "" {
			return Icon{}, apiError(404, "Open target icon is not available on "+s.options.Platform+": "+id, "OPEN_TARGET_ICON_UNAVAILABLE")
		}
	} else {
		d, exists := s.definition(id)
		if !exists {
			return Icon{}, apiError(404, "Unknown open target icon: "+id, "OPEN_TARGET_ICON_UNKNOWN")
		}
		list, err := s.list(ctx, false)
		if err != nil {
			return Icon{}, err
		}
		detected := false
		for _, item := range list.Targets {
			if item.ID == id {
				detected = true
				break
			}
		}
		if detected {
			path = s.resolveIcon(ctx, d)
		}
		if path == "" {
			return Icon{}, apiError(404, "Open target icon is not available on "+s.options.Platform+": "+id, "OPEN_TARGET_ICON_UNAVAILABLE")
		}
	}
	data, err := s.primitives.ConvertIconToPNG(ctx, path, normalized)
	if err != nil {
		return Icon{}, err
	}
	icon := Icon{"image/png", data}
	s.icons[key] = icon
	return icon, nil
}
func (s *Service) validatePath(value string) (resolvedPath, error) {
	if value == "~" || strings.HasPrefix(value, "~/") || s.options.Platform == "win32" && strings.HasPrefix(value, "~\\") {
		value = s.options.Home + value[1:]
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(s.options.CWD, value)
	}
	value = filepath.Clean(value)
	stat, err := os.Stat(value)
	if err != nil {
		return resolvedPath{}, apiError(400, "Path does not exist: "+value, "OPEN_TARGET_PATH_MISSING")
	}
	if !stat.IsDir() && !stat.Mode().IsRegular() {
		return resolvedPath{}, apiError(400, "Path is not a file or directory: "+value, "OPEN_TARGET_PATH_UNSUPPORTED")
	}
	return resolvedPath{value, stat.IsDir(), s.options.Platform != "win32" && stat.Mode().Perm()&0111 != 0}, nil
}
func applicationID(a Application) string {
	identity := a.AppPath
	if a.BundleID != nil && *a.BundleID != "" {
		identity = *a.BundleID
	}
	return "application:" + base64.RawURLEncoding.EncodeToString([]byte(identity))
}
func (s *Service) buildTarget(d definition) Target {
	return Target{ID: d.id, Kind: d.kind, Label: d.label, Icon: d.icon, IconURL: "/api/open-targets/icons/" + encodeURIComponent(d.id), Platform: s.options.Platform}
}
func (s *Service) buildApplication(a Application) Target {
	id := applicationID(a)
	isDefault := a.IsDefault
	return Target{ID: id, Kind: "application", Label: a.DisplayName, Icon: "application", IconURL: "/api/open-targets/icons/" + encodeURIComponent(id), Platform: s.options.Platform, AppPath: a.AppPath, BundleID: a.BundleID, IsDefault: &isDefault}
}
func (s *Service) buildSystem() Target {
	return Target{ID: "system-default", Kind: "system_default", Label: "System default", Icon: "system", Platform: s.options.Platform}
}
func (s *Service) canSystemOpen(ctx context.Context) bool {
	if s.options.Platform == "linux" {
		return s.primitives.CommandExists(ctx, "xdg-open")
	}
	return s.options.Platform == "darwin" || s.options.Platform == "win32"
}
func removeKey(order []string, key string) []string {
	for i, k := range order {
		if k == key {
			return append(order[:i], order[i+1:]...)
		}
	}
	return order
}
func (s *Service) applicationsForPath(ctx context.Context, target resolvedPath) ([]Application, error) {
	if cache, exists := s.paths[target.path]; exists && s.options.Now()-cache.cachedAt < *s.options.TTLMillis {
		return cache.applications, nil
	}
	apps, err := s.discoverApplications(ctx, target)
	if err != nil {
		return nil, err
	}
	s.pathOrder = append(removeKey(s.pathOrder, target.path), target.path)
	s.paths[target.path] = applicationCache{apps, s.options.Now()}
	for len(s.pathOrder) > maxCachedPaths {
		delete(s.paths, s.pathOrder[0])
		s.pathOrder = s.pathOrder[1:]
	}
	for _, a := range apps {
		id := applicationID(a)
		s.registryOrder = append(removeKey(s.registryOrder, id), id)
		s.registry[id] = a
	}
	for len(s.registryOrder) > maxRegisteredApplications {
		delete(s.registry, s.registryOrder[0])
		s.registryOrder = s.registryOrder[1:]
	}
	return apps, nil
}
