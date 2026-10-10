// This test-only executable requires the parent's disposable HOME/config.
package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/access"
	"github.com/NanmiCoder/cc-haha/backend/internal/h5access"
	"github.com/NanmiCoder/cc-haha/backend/internal/httpapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/migrations"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	weburl "github.com/nlnwa/whatwg-url/url"
)

type step struct {
	Method       string       `json:"method"`
	Path         string       `json:"path"`
	Segments     *[]string    `json:"segments"`
	Headers      [][2]string  `json:"headers"`
	Body         string       `json:"body"`
	TokenFrom    *int         `json:"tokenFrom"`
	BearerPrefix *string      `json:"bearerPrefix"`
	ReadError    *readFailure `json:"readError"`
}
type readFailure struct {
	Name    string `json:"name"`
	Message string `json:"message"`
}

func (failure readFailure) Read([]byte) (int, error) { return 0, failure }
func (failure readFailure) Close() error             { return nil }
func (failure readFailure) Error() string            { return failure.Message }
func (failure readFailure) ErrorName() string        { return failure.Name }

type fault struct {
	Operation          string `json:"operation"`
	Path               string `json:"path"`
	Message            string `json:"message"`
	Once               bool   `json:"once"`
	AfterManagedCommit bool   `json:"afterManagedCommit"`
	used               bool
}
type request struct {
	Home                  string                      `json:"home"`
	Steps                 []step                      `json:"steps"`
	Faults                []fault                     `json:"faults"`
	Environment           map[string]*string          `json:"environment"`
	Interfaces            []h5access.NetworkInterface `json:"interfaces"`
	ActivePort            *int                        `json:"activePort"`
	InitialRefresh        bool                        `json:"initialRefresh"`
	RandomErrorSize       *int                        `json:"randomErrorSize"`
	TrackWritePreparation bool                        `json:"trackWritePreparation"`
}
type event struct {
	Operation string   `json:"operation"`
	Paths     []string `json:"paths"`
}
type boundary struct {
	storage.FileSystem
	mu               sync.Mutex
	home             string
	events           []event
	faults           []fault
	apiStarted       bool
	managedCommitted bool
	writePreparation *[]string
}

func (boundary *boundary) before(operation string, paths ...string) error {
	names := []string{}
	for _, path := range paths {
		name, err := filepath.Rel(boundary.home, path)
		if err != nil || filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return errors.New("H5 HTTP fixture escaped disposable home")
		}
		names = append(names, filepath.ToSlash(name))
	}
	boundary.mu.Lock()
	defer boundary.mu.Unlock()
	boundary.events = append(boundary.events, event{operation, names})
	if boundary.apiStarted && boundary.writePreparation != nil && operation == "mkdir" {
		*boundary.writePreparation = append(*boundary.writePreparation, "mkdir:"+names[0])
	}
	for index := range boundary.faults {
		fault := &boundary.faults[index]
		if boundary.apiStarted && fault.Operation == operation && strings.HasPrefix(names[0], fault.Path) && (!fault.Once || !fault.used) && (!fault.AfterManagedCommit || boundary.managedCommitted) {
			fault.used = true
			return errors.New(fault.Message)
		}
	}
	return nil
}
func (boundary *boundary) ReadFile(path string) ([]byte, error) {
	if err := boundary.before("read", path); err != nil {
		return nil, err
	}
	return boundary.FileSystem.ReadFile(path)
}
func (boundary *boundary) WriteFile(path string, data []byte, mode fs.FileMode) error {
	if err := boundary.before("write", path); err != nil {
		return err
	}
	return boundary.FileSystem.WriteFile(path, data, mode)
}
func (boundary *boundary) MkdirAll(path string, mode fs.FileMode) error {
	if err := boundary.before("mkdir", path); err != nil {
		return err
	}
	return boundary.FileSystem.MkdirAll(path, mode)
}
func (boundary *boundary) Rename(source, target string) error {
	if err := boundary.before("rename", source, target); err != nil {
		return err
	}
	if err := boundary.FileSystem.Rename(source, target); err != nil {
		return err
	}
	if target == filepath.Join(boundary.home, ".claude", "cc-haha", "settings.json") {
		boundary.mu.Lock()
		boundary.managedCommitted = true
		boundary.mu.Unlock()
	}
	return nil
}
func (boundary *boundary) CopyFile(source, target string) error {
	if err := boundary.before("copy", source, target); err != nil {
		return err
	}
	return boundary.FileSystem.CopyFile(source, target)
}
func (boundary *boundary) Remove(path string) error {
	if err := boundary.before("unlink", path); err != nil {
		return err
	}
	return boundary.FileSystem.Remove(path)
}
func (boundary *boundary) access(path string) error {
	if err := boundary.before("access", path); err != nil {
		return err
	}
	_, err := os.Stat(path)
	return err
}

type deterministicRandom struct {
	mu         sync.Mutex
	calls      []int
	generation int
	errorSize  *int
	before     func(string)
}

func (random *deterministicRandom) Read(data []byte) (int, error) {
	random.mu.Lock()
	defer random.mu.Unlock()
	random.calls = append(random.calls, len(data))
	random.before(fmt.Sprintf("random:%d", len(data)))
	if random.errorSize != nil && *random.errorSize == len(data) {
		return 0, errors.New("fixture random denied")
	}
	if len(data) == 32 {
		random.generation++
		for index := range data {
			data[index] = byte(index + random.generation*17)
		}
	} else {
		for index := range data {
			data[index] = byte(index%3 + 1)
		}
	}
	return len(data), nil
}

type responseResult struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
	GraceMS int               `json:"graceMs"`
}

func run() error {
	var input request
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		return err
	}
	if input.Home == "" || !filepath.IsAbs(input.Home) || input.Home != os.Getenv("HOME") || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(input.Home, ".claude") {
		return errors.New("H5 HTTP fixture requires disposable HOME/config")
	}
	boundary := &boundary{FileSystem: storage.New(storage.Options{}).FileSystem(), home: input.Home, events: []event{}, faults: input.Faults}
	preparation := []string{}
	if input.TrackWritePreparation {
		boundary.writePreparation = &preparation
	}
	track := func(event string) {
		boundary.mu.Lock()
		defer boundary.mu.Unlock()
		if boundary.apiStarted && boundary.writePreparation != nil {
			preparation = append(preparation, event)
		}
	}
	clock := func() time.Time { track("clock"); return time.UnixMilli(1700000000000) }
	random := &deterministicRandom{calls: []int{}, errorSize: input.RandomErrorSize, before: track}
	warnings := 0
	var warningMu sync.Mutex
	store := storage.New(storage.Options{FS: boundary, Now: clock, Random: random, Warn: func(string) { warningMu.Lock(); warnings++; warningMu.Unlock() }})
	upgrader, err := migrations.New(migrations.Options{Store: store, Access: boundary.access, Now: clock, Random: random})
	if err != nil {
		return err
	}
	environment := map[string]string{}
	for key, value := range input.Environment {
		if value != nil {
			environment[key] = *value
		}
	}
	managed, err := settings.NewManaged(settings.Options{Store: store, HomeDir: input.Home, ConfigDir: filepath.Join(input.Home, ".claude"), Environment: func() map[string]string { return environment }, EnsureMigrated: upgrader.SettingsCallback})
	if err != nil {
		return err
	}
	port := 3456
	if input.ActivePort != nil {
		port = *input.ActivePort
	}
	service, err := h5access.New(h5access.Options{Managed: managed, Environment: func() map[string]string { return environment }, NetworkInterfaces: func() []h5access.NetworkInterface { return input.Interfaces }, ServerPort: func() int { return port }, Random: random})
	if err != nil {
		return err
	}
	cache, err := httpapi.NewH5GraceCache(service)
	if err != nil {
		return err
	}
	diagnostics := []map[string]string{}
	handler, err := httpapi.NewH5Access(httpapi.H5AccessOptions{Service: service, GraceCache: cache, OnUnhandled: func(event httpapi.UnhandledError) {
		diagnostics = append(diagnostics, map[string]string{"type": event.Type, "severity": event.Severity, "summary": event.Summary})
		fmt.Fprintln(os.Stderr, "[Server] Unexpected error:", event.Error)
		_, _ = os.Stderr.Write(debug.Stack())
	}})
	if err != nil {
		return err
	}
	if input.InitialRefresh {
		cache.Refresh(context.Background())
	}
	results := []responseResult{}
	policy := []map[string]any{}
	for _, step := range input.Steps {
		boundary.mu.Lock()
		boundary.apiStarted = true
		boundary.mu.Unlock()
		data, err := hex.DecodeString(step.Body)
		if err != nil {
			return err
		}
		req := httptest.NewRequest(step.Method, "http://127.0.0.1:3456"+step.Path, bytes.NewReader(data))
		for _, header := range step.Headers {
			req.Header.Add(header[0], strings.Trim(header[1], " \t\r\n"))
		}
		if step.TokenFrom != nil {
			if *step.TokenFrom < 0 || *step.TokenFrom >= len(results) {
				return errors.New("H5 HTTP fixture tokenFrom missing result")
			}
			raw, err := hex.DecodeString(results[*step.TokenFrom].Body)
			if err != nil {
				return err
			}
			var previous struct {
				Token *string `json:"token"`
			}
			if err := json.Unmarshal(raw, &previous); err != nil || previous.Token == nil {
				return errors.New("H5 HTTP fixture tokenFrom is not a successful token result")
			}
			prefix := "Bearer "
			if step.BearerPrefix != nil {
				prefix = *step.BearerPrefix
			}
			req.Header.Add("Authorization", prefix+*previous.Token)
		}
		if step.ReadError != nil {
			req.Body = *step.ReadError
		}
		url, err := weburl.Parse(req.URL.String())
		if err != nil {
			return err
		}
		parts := []string{}
		for _, part := range strings.Split(url.Pathname(), "/") {
			if part != "" {
				parts = append(parts, part)
			}
		}
		entry := map[string]any{"pathname": url.Pathname(), "control": access.IsH5AccessControlPath(url.Pathname()), "requiresLocalCredential": access.RequiresLocalAccessCredential(url.Pathname(), access.H5Context{ClientAddress: "127.0.0.1", LocalAccessTokenConfigured: true, LocalAccessAuthorized: false})}
		if len(parts) > 1 {
			entry["resource"] = parts[1]
		}
		policy = append(policy, entry)
		response := httptest.NewRecorder()
		if step.Segments != nil {
			handler.Handle(response, req, *step.Segments)
		} else {
			handler.ServeHTTP(response, req)
		}
		headers := map[string]string{}
		for name, values := range response.Header() {
			headers[strings.ToLower(name)] = strings.Join(values, ", ")
		}
		results = append(results, responseResult{response.Code, headers, hex.EncodeToString(response.Body.Bytes()), cache.Milliseconds()})
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	output := map[string]any{"results": results, "trace": boundary.events, "warningCount": warnings, "diagnostics": diagnostics, "randomCalls": random.calls, "policy": policy}
	if input.TrackWritePreparation {
		output["writePreparation"] = preparation
	}
	return encoder.Encode(output)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
