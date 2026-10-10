// Package pluginzip is the native implementation of the plugin ZIP cache and
// extraction primitives. The caller owns plugin registry and corrupt-cache
// deletion policy; this package does not load a TS runtime or user settings.
package pluginzip

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

type Operation struct {
	Kind string
	Path string
	To   string
	Size int64
	Mode os.FileMode
}
type Options struct {
	TempDir     string
	Home        string
	Environment map[string]string
	Platform    string
	Random      func([]byte) (int, error)
	Now         func() time.Time
	// Hooks observe actual native filesystem operations and optionally inject a
	// failure before I/O. Default production paths always use the real FS.
	Before func(Operation) error
	After  func(Operation, error)
}
type sessionPromise struct {
	done chan struct{}
	path string
	err  error
}
type Service struct {
	mu      sync.Mutex
	options Options
	path    string
	pending *sessionPromise
}

func New(options Options) (*Service, error) {
	if options.TempDir == "" || !filepath.IsAbs(options.TempDir) {
		return nil, errors.New("Plugin ZIP cache requires an explicit absolute temporary directory")
	}
	if options.Random == nil {
		options.Random = rand.Read
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Platform == "" {
		options.Platform = runtime.GOOS
	}
	env := map[string]string{}
	for k, v := range options.Environment {
		env[k] = v
	}
	options.Environment = env
	return &Service{options: options}, nil
}
func (s *Service) before(ctx context.Context, op Operation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.options.Before != nil {
		return s.options.Before(op)
	}
	return nil
}
func (s *Service) after(op Operation, err error) {
	if s.options.After != nil {
		s.options.After(op, err)
	}
}
func (s *Service) mkdir(ctx context.Context, path string) error {
	op := Operation{Kind: "mkdir", Path: path, Mode: 0777}
	if err := s.before(ctx, op); err != nil {
		return err
	}
	err := os.MkdirAll(path, 0777)
	// Original getFsImplementation().mkdir swallows EEXIST even when the leaf
	// is a regular file. Recursive Go mkdir reports ENOTDIR for that leaf.
	if err != nil {
		if info, statErr := os.Stat(path); statErr == nil && !info.IsDir() {
			err = nil
		}
	}
	s.after(op, err)
	return nodeIOError(err, "mkdir", path, "")
}
func (s *Service) read(ctx context.Context, path string) ([]byte, error) {
	op := Operation{Kind: "read", Path: path}
	if err := s.before(ctx, op); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	s.after(op, err)
	return data, nodeIOError(err, "open", path, "")
}
func (s *Service) write(ctx context.Context, path string, data []byte) error {
	op := Operation{Kind: "write", Path: path, Size: int64(len(data)), Mode: 0666}
	if err := s.before(ctx, op); err != nil {
		return err
	}
	err := os.WriteFile(path, data, 0666)
	s.after(op, err)
	return nodeIOError(err, "open", path, "")
}
func (s *Service) rename(ctx context.Context, path, to string) error {
	op := Operation{Kind: "rename", Path: path, To: to}
	if err := s.before(ctx, op); err != nil {
		return err
	}
	err := os.Rename(path, to)
	s.after(op, err)
	if errors.Is(err, os.ErrExist) {
		if info, statErr := os.Stat(to); statErr == nil && info.IsDir() {
			return &IOError{Code: "EISDIR", Message: fmt.Sprintf("EISDIR: illegal operation on a directory, rename '%s' -> '%s'", path, to), Cause: err}
		}
	}
	return nodeIOError(err, "rename", path, to)
}
func (s *Service) remove(ctx context.Context, path string, recursive bool) error {
	op := Operation{Kind: "remove", Path: path}
	if recursive {
		op.Kind = "remove-tree"
	}
	if err := s.before(ctx, op); err != nil {
		return err
	}
	var err error
	if recursive {
		err = os.RemoveAll(path)
	} else {
		err = os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			err = nil
		}
	}
	s.after(op, err)
	return nodeIOError(err, "unlink", path, "")
}
func (s *Service) randomHex(length int) (string, error) {
	buffer := make([]byte, length)
	n, err := s.options.Random(buffer)
	if err != nil {
		return "", err
	}
	if n != len(buffer) {
		return "", errors.New("random source returned insufficient bytes")
	}
	return hex.EncodeToString(buffer), nil
}

func (s *Service) SessionPath(ctx context.Context) (string, error) {
	s.mu.Lock()
	if s.path != "" {
		path := s.path
		s.mu.Unlock()
		return path, nil
	}
	p := s.pending
	if p == nil {
		p = &sessionPromise{done: make(chan struct{})}
		s.pending = p
		go func() {
			suffix, err := s.randomHex(8)
			path := ""
			if err == nil {
				path = filepath.Join(s.options.TempDir, "claude-plugin-session-"+suffix)
				err = s.mkdir(context.Background(), path)
			}
			s.mu.Lock()
			p.path, p.err = path, err
			if err != nil {
				p.path = ""
			}
			if err == nil {
				s.path = path
			}
			close(p.done)
			s.mu.Unlock()
		}()
	}
	s.mu.Unlock()
	select {
	case <-p.done:
		return p.path, p.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// Cleanup matches the original best-effort cleanup: a failed removal still
// clears the cached path/promise; pending creation with no path is left alone.
func (s *Service) Cleanup(ctx context.Context) error {
	s.mu.Lock()
	path := s.path
	s.mu.Unlock()
	if path == "" {
		return nil
	}
	_ = s.remove(ctx, path, true)
	s.mu.Lock()
	s.path = ""
	s.pending = nil
	s.mu.Unlock()
	return nil
}

// Reset clears cache ownership without deleting its directory, matching the
// original testing primitive. Runtime shutdown should call Cleanup instead.
func (s *Service) Reset() { s.mu.Lock(); s.path = ""; s.pending = nil; s.mu.Unlock() }

func (s *Service) AtomicWrite(ctx context.Context, target string, data []byte) error {
	dir := filepath.Dir(target)
	if err := s.mkdir(ctx, dir); err != nil {
		return err
	}
	suffix, err := s.randomHex(4)
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "."+filepath.Base(target)+".tmp."+suffix)
	if err = s.write(ctx, tmp, data); err == nil {
		err = s.rename(ctx, tmp, target)
	}
	if err != nil {
		_ = s.remove(context.Background(), tmp, false)
	}
	return err
}
func (s *Service) Convert(ctx context.Context, dirPath, zipPath string) error {
	data, err := s.Create(ctx, dirPath)
	if err != nil {
		return err
	}
	if err = s.AtomicWrite(ctx, zipPath, data); err != nil {
		return err
	}
	return s.remove(ctx, dirPath, true)
}
func sanitize(value string, allowAt bool) string {
	var b strings.Builder
	for _, c := range utf16.Encode([]rune(value)) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || allowAt && c == '@' {
			b.WriteByte(byte(c))
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}
func MarketplaceRelativePath(name string) string {
	return filepath.Join("marketplaces", sanitize(name, false)+".json")
}
func SupportedMarketplaceSource(source string) bool {
	return source == "github" || source == "git" || source == "url" || source == "settings"
}
func PluginDirectoryName(id string) string { return sanitize(id, true) }
func (s *Service) ExtractPlugin(ctx context.Context, zipPath, pluginID string) (string, error) {
	root, err := s.SessionPath(ctx)
	if err != nil {
		return "", err
	}
	target := filepath.Join(root, PluginDirectoryName(pluginID))
	if err = s.Extract(ctx, zipPath, target); err != nil {
		return "", err
	}
	return target, nil
}
func (s *Service) CacheEnabled() bool {
	v := strings.TrimSpace(strings.ToLower(s.options.Environment["CLAUDE_CODE_PLUGIN_USE_ZIP_CACHE"]))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}
func (s *Service) CachePath() string {
	if !s.CacheEnabled() {
		return ""
	}
	path := s.options.Environment["CLAUDE_CODE_PLUGIN_CACHE_DIR"]
	if path == "~" || strings.HasPrefix(path, "~/") || (s.options.Platform == "windows" || s.options.Platform == "win32") && strings.HasPrefix(path, `~\`) {
		return s.options.Home + path[1:]
	}
	return path
}
func (s *Service) CacheFile(name string) (string, error) {
	path := s.CachePath()
	if path == "" {
		return "", errors.New("Plugin zip cache is not enabled")
	}
	return filepath.Join(path, name), nil
}

type IOError struct {
	Code    string
	Message string
	Cause   error
}

func (e *IOError) Error() string { return e.Message }
func (e *IOError) Unwrap() error { return e.Cause }
func nodeIOError(err error, operation, path, to string) error {
	if err == nil {
		return nil
	}
	code, message := "", ""
	switch {
	case errors.Is(err, os.ErrNotExist):
		code, message = "ENOENT", "no such file or directory"
	case errors.Is(err, os.ErrPermission):
		code, message = "EACCES", "permission denied"
	case errors.Is(err, os.ErrExist):
		code, message = "EEXIST", "file already exists"
	default:
		code, message = platformIOError(err)
	}
	if code == "" {
		return err
	}
	text := fmt.Sprintf("%s: %s, %s '%s'", code, message, operation, path)
	if to != "" {
		text += " -> '" + to + "'"
	}
	return &IOError{Code: code, Message: text, Cause: err}
}
