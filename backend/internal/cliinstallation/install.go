package cliinstallation

import (
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/releasenotes"
)

type Options struct {
	CWD, PackageName                       string
	Run                                    func(context.Context, string, []string, string) (CommandResult, error)
	Home                                   string
	Environment                            map[string]string
	Platform, Architecture, CurrentVersion string
	MinimumVersion                         string
	Repository                             RepositoryClient
	Now                                    func() time.Time
	// SaveInstallMethod runs only after verified activation. The shared Source
	// global config writer owns its backup, unknown fields and write semantics.
	SaveInstallMethod func() error
}
type Directories struct{ Versions, Staging, Locks, Executable string }
type Result struct {
	Version, Executable string
	Updated, LockFailed bool
}
type Installer struct {
	options Options
	dirs    Directories
	name    string
}

func New(options Options) (*Installer, error) {
	if !filepath.IsAbs(options.Home) {
		return nil, errors.New("CLI installer requires an explicit absolute HOME")
	}
	if options.Platform == "" {
		options.Platform = runtime.GOOS
	}
	if options.Architecture == "" {
		options.Architecture = runtime.GOARCH
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	name, err := BinaryName(options.Platform, options.Architecture)
	if err != nil {
		return nil, err
	}
	resolve := func(key, fallback string) string {
		if value, present := options.Environment[key]; present {
			return value
		}
		return filepath.Join(options.Home, fallback)
	}
	command := "claude"
	if options.Platform == "windows" || options.Platform == "win32" {
		command = "claude.exe"
	}
	dirs := Directories{filepath.Join(resolve("XDG_DATA_HOME", ".local/share"), "claude", "versions"), filepath.Join(resolve("XDG_CACHE_HOME", ".cache"), "claude", "staging"), filepath.Join(resolve("XDG_STATE_HOME", ".local/state"), "claude", "locks"), filepath.Join(options.Home, ".local/bin", command)}
	return &Installer{options, dirs, name}, nil
}
func (s *Installer) Directories() Directories { return s.dirs }

func (s *Installer) validateBinary(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return errors.New("CLI executable is not a regular nonempty file")
	}
	build, err := buildinfo.ReadFile(path)
	if err != nil || build.Path != "github.com/NanmiCoder/cc-haha/backend/cmd/claude-haha" {
		return errors.New("CLI executable is not the Go application runtime")
	}
	platform := s.options.Platform
	if platform == "win32" {
		platform = "windows"
	}
	arch := s.options.Architecture
	if arch == "x64" {
		arch = "amd64"
	}
	settings := map[string]string{}
	for _, setting := range build.Settings {
		settings[setting.Key] = setting.Value
	}
	if settings["GOOS"] != platform || settings["GOARCH"] != arch {
		return errors.New("CLI executable has a different platform or architecture")
	}
	if platform != "windows" && info.Mode().Perm()&0111 == 0 {
		return errors.New("CLI executable is not executable")
	}
	return nil
}

func (s *Installer) Install(ctx context.Context, target string, force bool) (Result, error) {
	release, err := s.options.Repository.Resolve(ctx, target)
	if err != nil {
		return Result{}, err
	}
	version := strings.TrimPrefix(release.Tag, "v")
	result := Result{Version: version, Executable: s.dirs.Executable}
	if !force && s.options.MinimumVersion != "" {
		comparison, err := releasenotes.Compare(version, s.options.MinimumVersion)
		if err != nil {
			return result, err
		}
		if comparison < 0 {
			return result, nil
		}
	}
	path := filepath.Join(s.dirs.Versions, version)
	if !force && version == s.options.CurrentVersion && s.validateBinary(path) == nil && s.validateBinary(s.dirs.Executable) == nil {
		return result, nil
	}
	for _, directory := range []string{s.dirs.Versions, s.dirs.Staging, s.dirs.Locks, filepath.Dir(s.dirs.Executable)} {
		if err = os.MkdirAll(directory, 0755); err != nil {
			return result, err
		}
	}
	releaseLock, err := s.acquire(ctx, version, force)
	if err != nil {
		return result, err
	}
	if releaseLock == nil {
		result.LockFailed = true
		return result, nil
	}
	defer releaseLock()
	if force || s.validateBinary(path) != nil {
		asset, expected, err := s.options.Repository.Manifest(ctx, release, s.name)
		if err != nil {
			return result, err
		}
		body, err := s.options.Repository.fetch(ctx, asset.URL, expected.Size)
		if err != nil {
			return result, err
		}
		hash := sha256.Sum256(body)
		if int64(len(body)) != expected.Size || !strings.EqualFold(hex.EncodeToString(hash[:]), expected.SHA256) {
			return result, errors.New("Go CLI binary checksum does not match its release manifest")
		}
		staged, err := os.CreateTemp(s.dirs.Staging, version+"-*")
		if err != nil {
			return result, err
		}
		defer os.Remove(staged.Name())
		_, err = staged.Write(body)
		if err == nil {
			err = staged.Chmod(0755)
		}
		if err == nil {
			err = staged.Sync()
		}
		closeErr := staged.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return result, err
		}
		if err = s.validateBinary(staged.Name()); err != nil {
			return result, err
		}
		if err = atomicCopy(staged.Name(), path); err != nil {
			return result, err
		}
		result.Updated = true
	}
	if err = s.activate(path); err != nil {
		return result, err
	}
	if err = s.validateBinary(s.dirs.Executable); err != nil {
		return result, fmt.Errorf("Failed to create executable at %s: %w", s.dirs.Executable, err)
	}
	if s.options.SaveInstallMethod != nil {
		if err = s.options.SaveInstallMethod(); err != nil {
			return result, err
		}
	}
	return result, nil
}

func atomicCopy(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	file, err := os.CreateTemp(filepath.Dir(destination), filepath.Base(destination)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = io.Copy(file, input)
	if err == nil {
		err = file.Chmod(0755)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(file.Name(), destination)
}

func (s *Installer) activate(path string) error {
	if info, err := os.Stat(s.dirs.Executable); err == nil && info.IsDir() {
		// Source removes only an empty obsolete launcher directory.
		if err = os.Remove(s.dirs.Executable); err != nil {
			return err
		}
	}
	if s.options.Platform == "windows" || s.options.Platform == "win32" {
		if _, err := os.Stat(s.dirs.Executable); errors.Is(err, os.ErrNotExist) {
			return atomicCopy(path, s.dirs.Executable)
		}
		backup := fmt.Sprintf("%s.old.%d", s.dirs.Executable, s.options.Now().UnixMilli())
		if err := os.Rename(s.dirs.Executable, backup); err != nil {
			return err
		}
		if err := atomicCopy(path, s.dirs.Executable); err != nil {
			if restore := os.Rename(backup, s.dirs.Executable); restore != nil {
				return fmt.Errorf("Failed to restore old executable: %v (copy: %w)", restore, err)
			}
			return err
		}
		_ = os.Remove(backup)
		return nil
	}
	link := fmt.Sprintf("%s.tmp.%d.%d", s.dirs.Executable, os.Getpid(), s.options.Now().UnixNano())
	defer os.Remove(link)
	if err := os.Symlink(path, link); err != nil {
		return err
	}
	return os.Rename(link, s.dirs.Executable)
}

func (s *Installer) acquire(ctx context.Context, version string, force bool) (func(), error) {
	if environmentTruthy(s.options.Environment["ENABLE_LOCKLESS_UPDATES"]) {
		return func() {}, nil
	}
	path := filepath.Join(s.dirs.Locks, version+".lock")
	if force {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	for attempt := 0; attempt < 4; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if s.pidLocking() {
			if release := s.acquirePID(path, version); release != nil {
				return release, nil
			}
			if attempt < 3 {
				timer := time.NewTimer(time.Second * time.Duration(1<<attempt))
				select {
				case <-ctx.Done():
					timer.Stop()
					return nil, ctx.Err()
				case <-timer.C:
				}
			}
			continue
		}
		release, err := s.acquireMtime(path)
		if err == nil {
			return release, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if attempt < 3 {
			timer := time.NewTimer(time.Second * time.Duration(1<<attempt))
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return nil, nil
}
