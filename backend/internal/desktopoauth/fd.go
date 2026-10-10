package desktopoauth

import (
	"context"
	"encoding/json"
	"io/fs"
	"math"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// CredentialDescriptorOptions carries only the original synchronous OS file
// primitives. The caller owns the one-shot cache and chooses its credential
// slot; this helper never reads another account or creates an OAuth service.
type CredentialDescriptorOptions struct {
	Descriptor, Platform, FallbackPath string
	Remote                             bool
	DescriptorPath                     func(int64) string
	ReadFile                           func(string) ([]byte, error)
	MkdirAll                           func(string, fs.FileMode) error
	WriteFile                          func(string, []byte, fs.FileMode) error
}

func ReadCredentialDescriptor(o CredentialDescriptorOptions) string {
	fallback := func() string {
		value, err := o.ReadFile(o.FallbackPath)
		if err != nil {
			return ""
		}
		return trimJS(string(protocol.DecodeJavaScriptUTF8(value)))
	}
	if o.Descriptor == "" {
		return fallback()
	}
	name, valid := descriptorName(o.Descriptor)
	if !valid {
		return ""
	}
	path := ""
	fd, numericError := strconv.ParseInt(name, 10, 64)
	if o.DescriptorPath != nil && numericError == nil {
		path = o.DescriptorPath(fd)
	} else if o.Platform == "darwin" || o.Platform == "freebsd" {
		path = "/dev/fd/" + name
	} else {
		path = "/proc/self/fd/" + name
	}
	value, err := o.ReadFile(path)
	if err != nil {
		return fallback()
	}
	token := trimJS(string(protocol.DecodeJavaScriptUTF8(value)))
	// A successfully read empty descriptor deliberately does not fall back.
	if token != "" && o.Remote && o.MkdirAll != nil && o.WriteFile != nil {
		if o.MkdirAll(filepath.Dir(o.FallbackPath), 0700) == nil {
			_ = o.WriteFile(o.FallbackPath, []byte(token), 0600)
		}
	}
	return token
}

// Source parseInt produces a Number, including Infinity for an overflowing
// decimal prefix. Such a descriptor still attempts a named OS read and may use
// the disk fallback; it is different from NaN, which never falls back.
func descriptorName(value string) (string, bool) {
	value = trimJS(value)
	end := 0
	if strings.HasPrefix(value, "+") || strings.HasPrefix(value, "-") {
		end++
	}
	start := end
	for end < len(value) && value[end] >= '0' && value[end] <= '9' {
		end++
	}
	if end == start {
		return "", false
	}
	number, _ := strconv.ParseFloat(value[:end], 64)
	if math.IsInf(number, 1) {
		return "Infinity", true
	}
	if math.IsInf(number, -1) {
		return "-Infinity", true
	}
	quoted, err := protocol.JavaScriptStringJSON([]byte(strconv.FormatFloat(number, 'g', -1, 64)))
	var name string
	if err != nil || json.Unmarshal(quoted, &name) != nil {
		return "", false
	}
	return name, true
}

// readOAuthTokenFD preserves the one-shot FD cache and CCR file fallback.
// Empty/invalid descriptors cache null; an I/O failure tries the file fallback.
func (s *Service) readOAuthTokenFD(context.Context) (string, error) {
	s.fdOnce.Do(func() {
		env := s.options.Environment()
		path := s.options.CCROAuthTokenPath
		if path == "" {
			path = "/home/claude/.claude/remote/.oauth_token"
		}
		files := s.options.Store.FileSystem()
		s.fdToken = ReadCredentialDescriptor(CredentialDescriptorOptions{Descriptor: env["CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR"], Platform: s.options.Platform, FallbackPath: path, Remote: envTruthy(env["CLAUDE_CODE_REMOTE"]), DescriptorPath: s.options.TokenFDPath, ReadFile: files.ReadFile, MkdirAll: files.MkdirAll, WriteFile: files.WriteFile})
	})
	return s.fdToken, nil
}

// JS parseInt(base 10) consumes a signed decimal prefix, not the whole value.
func parseDescriptor(value string) (int64, bool) {
	value = trimJS(value)
	if value == "" {
		return 0, false
	}
	end := 0
	if strings.HasPrefix(value, "+") || strings.HasPrefix(value, "-") {
		end++
	}
	start := end
	for end < len(value) && value[end] >= '0' && value[end] <= '9' {
		end++
	}
	if start == end {
		return 0, false
	}
	fd, err := strconv.ParseInt(value[:end], 10, 64)
	return fd, err == nil
}
