package remoteio

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// IngressAuth owns the process's one-read session ingress credential cache,
// shared by remote I/O and MCP WebSocket headers without a second FD read.
type IngressAuth struct {
	mu                  sync.Mutex
	env                 map[string]string
	tokenRead           bool
	cachedToken         string
	credentialDirectory string
}

func NewIngressAuth(environment map[string]string) *IngressAuth {
	return NewIngressAuthWithDirectory(environment, "")
}
func NewIngressAuthWithDirectory(environment map[string]string, directory string) *IngressAuth {
	if directory == "" {
		directory = "/home/claude/.claude/remote"
	}
	a := &IngressAuth{env: map[string]string{}, credentialDirectory: directory}
	a.UpdateEnvironment(environment)
	return a
}
func (a *IngressAuth) UpdateEnvironment(variables map[string]string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for key, value := range variables {
		a.env[key] = value
	}
}

// Token matches the original env -> once-read FD/file cache. A refreshed
// process environment takes precedence over that cache on every reconnect.
func (a *IngressAuth) Token() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if token := a.env["CLAUDE_CODE_SESSION_ACCESS_TOKEN"]; token != "" {
		return token
	}
	if a.tokenRead {
		return a.cachedToken
	}
	a.tokenRead = true
	file, fileDefined := a.env["CLAUDE_SESSION_INGRESS_TOKEN_FILE"]
	if !fileDefined {
		file = filepath.Join(a.credentialDirectory, ".session_ingress_token")
	}
	if fdEnv := a.env["CLAUDE_CODE_WEBSOCKET_AUTH_FILE_DESCRIPTOR"]; fdEnv != "" {
		fd, ok := parseInteger(fdEnv)
		if !ok {
			return ""
		}
		prefix := "/proc/self/fd/"
		if runtime.GOOS == "darwin" || runtime.GOOS == "freebsd" {
			prefix = "/dev/fd/"
		}
		if data, err := os.ReadFile(prefix + strconv.Itoa(fd)); err == nil {
			a.cachedToken = protocol.TrimJavaScriptString(string(data))
			if a.cachedToken != "" && truthy(a.env["CLAUDE_CODE_REMOTE"]) {
				// The Source CCR FD reader best-effort persists only successful
				// nonempty FD bytes to its well-known subprocess token location.
				if os.MkdirAll(a.credentialDirectory, 0700) == nil {
					_ = os.WriteFile(filepath.Join(a.credentialDirectory, ".session_ingress_token"), []byte(a.cachedToken), 0600)
				}
			}
			return a.cachedToken
		}
	}
	data, _ := os.ReadFile(file)
	a.cachedToken = protocol.TrimJavaScriptString(string(data))
	return a.cachedToken
}

func (s *Service) ingressToken() string { return s.options.IngressAuth.Token() }

func parseInteger(raw string) (int, bool) {
	text := protocol.TrimJavaScriptString(raw)
	i := 0
	if strings.HasPrefix(text, "+") || strings.HasPrefix(text, "-") {
		i++
	}
	start := i
	for i < len(text) && text[i] >= '0' && text[i] <= '9' {
		i++
	}
	if start == i {
		return 0, false
	}
	n, err := strconv.Atoi(text[:i])
	return n, err == nil
}

func (s *Service) headers(cookie bool) http.Header {
	h := http.Header{}
	if token := s.ingressToken(); token != "" {
		if cookie && strings.HasPrefix(token, "sk-ant-sid") {
			h.Set("Cookie", "sessionKey="+token)
			if org := s.environment("CLAUDE_CODE_ORGANIZATION_UUID"); org != "" {
				h.Set("X-Organization-Uuid", org)
			}
		} else {
			h.Set("Authorization", "Bearer "+token)
		}
	}
	if version := s.environment("CLAUDE_CODE_ENVIRONMENT_RUNNER_VERSION"); version != "" {
		h.Set("x-environment-runner-version", version)
	}
	if cookie {
		h.Set("anthropic-version", "2023-06-01")
		if s.options.UserAgent != "" {
			h.Set("User-Agent", s.options.UserAgent)
		}
	}
	return h
}

func (s *Service) UpdateEnvironment(variables map[string]string) {
	s.options.IngressAuth.UpdateEnvironment(variables)
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, value := range variables {
		s.env[key] = value
	}
}
func (s *Service) environment(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.env[key]
}

func (s *Service) environmentSnapshot() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make(map[string]string, len(s.env))
	for key, value := range s.env {
		result[key] = value
	}
	return result
}

type InitError struct{ Reason string }

func (err *InitError) Error() string { return fmt.Sprintf("CCRClient init failed: %s", err.Reason) }
