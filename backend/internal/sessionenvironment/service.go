// Package sessionenvironment owns the process memo and physical Hook env files
// consumed by the original Bash provider. It does not parse or execute shell.
package sessionenvironment

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type Service struct {
	mu                   sync.Mutex
	configHome, platform string
	loaded               bool
	value                *string
}

func New(configHome, platform string) (*Service, error) {
	if !filepath.IsAbs(configHome) {
		return nil, errors.New("session environment requires actual absolute config home")
	}
	return &Service{configHome: configHome, platform: platform}, nil
}

func (s *Service) directory(sid string) (string, error) {
	if sid == "" || filepath.Base(sid) != sid || sid == "." || sid == ".." {
		return "", errors.New("invalid original session environment identity")
	}
	path := filepath.Join(s.configHome, "session-env", sid)
	if err := os.MkdirAll(path, 0755); err != nil {
		return "", err
	}
	return path, nil
}

func (s *Service) HookEnvFile(sid, event string, index int) (string, error) {
	switch event {
	case "Setup", "SessionStart", "CwdChanged", "FileChanged":
	default:
		return "", errors.New("invalid Hook environment event")
	}
	if index < 0 {
		return "", errors.New("invalid Hook environment index")
	}
	directory, err := s.directory(sid)
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, strings.ToLower(event)+"-hook-"+strconv.Itoa(index)+".sh"), nil
}

var hookEnvPattern = regexp.MustCompile(`^(setup|sessionstart|cwdchanged|filechanged)-hook-([0-9]+)\.sh$`)
var hookEnvPriority = map[string]int{"setup": 0, "sessionstart": 1, "cwdchanged": 2, "filechanged": 3}

func SortHookEnvFiles(files []string) {
	parts := func(name string) (int, float64) {
		match := hookEnvPattern.FindStringSubmatch(name)
		if match == nil {
			return 99, 0
		}
		index, err := strconv.ParseFloat(match[2], 64)
		if err != nil && !math.IsInf(index, 1) {
			index = 0
		}
		return hookEnvPriority[match[1]], index
	}
	sort.SliceStable(files, func(i, j int) bool {
		a, x := parts(files[i])
		b, y := parts(files[j])
		if a != b {
			return a < b
		}
		return x < y
	})
}

func readDirectory(path string) ([]string, error) {
	directory, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	entries, err := directory.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names, nil
}
func trimmedScript(bytes []byte) string {
	return strings.TrimFunc(string(protocol.DecodeJavaScriptUTF8(bytes)), func(value rune) bool { return protocol.IsJavaScriptWhitespace(value) })
}

// Script returns a clone of the same memoized nullable value. Source's null
// cache does not check disk again until Invalidate, including across SID changes.
func (s *Service) Script(sid string, environment map[string]string) (*string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.platform == "windows" || s.platform == "win32" {
		return nil, nil
	}
	if s.loaded {
		if s.value == nil {
			return nil, nil
		}
		value := *s.value
		return &value, nil
	}
	scripts := []string{}
	if path := environment["CLAUDE_ENV_FILE"]; path != "" {
		if bytes, err := os.ReadFile(path); err == nil {
			if value := trimmedScript(bytes); value != "" {
				scripts = append(scripts, value)
			}
		}
	}
	directory, err := s.directory(sid)
	if err != nil {
		return nil, err
	}
	files, err := readDirectory(directory)
	if err == nil {
		filtered := []string{}
		for _, name := range files {
			if hookEnvPattern.MatchString(name) {
				filtered = append(filtered, name)
			}
		}
		SortHookEnvFiles(filtered)
		for _, name := range filtered {
			if bytes, err := os.ReadFile(filepath.Join(directory, name)); err == nil {
				if value := trimmedScript(bytes); value != "" {
					scripts = append(scripts, value)
				}
			}
		}
	}
	s.loaded = true
	if len(scripts) == 0 {
		s.value = nil
		return nil, nil
	}
	value := strings.Join(scripts, "\n")
	s.value = &value
	out := value
	return &out, nil
}

func (s *Service) Invalidate() { s.mu.Lock(); s.loaded = false; s.value = nil; s.mu.Unlock() }

// ClearCwd preserves Setup/SessionStart files and, like Source, doesn't create
// a new cache value or invalidate it itself; the executor owns invalidation.
func (s *Service) ClearCwd(sid string) {
	directory, err := s.directory(sid)
	if err != nil {
		return
	}
	names, err := readDirectory(directory)
	if err != nil {
		return
	}
	for _, name := range names {
		if (strings.HasPrefix(name, "filechanged-hook-") || strings.HasPrefix(name, "cwdchanged-hook-")) && hookEnvPattern.MatchString(name) {
			file, err := os.OpenFile(filepath.Join(directory, name), os.O_WRONLY|os.O_TRUNC, 0666)
			if err == nil {
				_ = file.Close()
			}
		}
	}
}
