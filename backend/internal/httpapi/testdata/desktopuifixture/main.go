package main

import (
	"bytes"
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
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/access"
	"github.com/NanmiCoder/cc-haha/backend/internal/httpapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/migrations"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/uipreferences"
)

type step struct {
	Method    string       `json:"method"`
	Path      string       `json:"path"`
	Segments  *[]string    `json:"segments"`
	Headers   [][2]string  `json:"headers"`
	Body      string       `json:"body"`
	ReadError *readFailure `json:"readError"`
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
	Operation string `json:"operation"`
	Path      string `json:"path"`
	Message   string `json:"message"`
	Once      bool   `json:"once"`
	used      bool
}
type request struct {
	Home     string  `json:"home"`
	Steps    []step  `json:"steps"`
	Faults   []fault `json:"faults"`
	PetToken string  `json:"petToken"`
}
type event struct {
	Operation string   `json:"operation"`
	Paths     []string `json:"paths"`
}
type boundary struct {
	storage.FileSystem
	home   string
	events []event
	faults []fault
}

func (boundary *boundary) before(operation string, paths ...string) error {
	names := []string{}
	for _, path := range paths {
		name, err := filepath.Rel(boundary.home, path)
		if err != nil || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return errors.New("UI fixture escaped disposable home")
		}
		names = append(names, filepath.ToSlash(name))
	}
	boundary.events = append(boundary.events, event{operation, names})
	for index := range boundary.faults {
		fault := &boundary.faults[index]
		if fault.Operation == operation && strings.HasPrefix(names[0], fault.Path) && (!fault.Once || !fault.used) {
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
	return boundary.FileSystem.Rename(source, target)
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
func run() error {
	var input request
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		return err
	}
	if input.Home == "" || input.Home != os.Getenv("HOME") || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(input.Home, ".claude") {
		return errors.New("UI fixture requires disposable HOME/config")
	}
	boundary := &boundary{FileSystem: storage.New(storage.Options{}).FileSystem(), home: input.Home, events: []event{}, faults: input.Faults}
	warnings := []string{}
	clock := func() time.Time { return time.UnixMilli(1700000000000) }
	random := bytes.NewReader(bytes.Repeat([]byte{1, 2, 3}, 100000))
	store := storage.New(storage.Options{FS: boundary, Now: clock, Random: random, Warn: func(message string) { warnings = append(warnings, message) }})
	upgrader, err := migrations.New(migrations.Options{Store: store, Access: boundary.access, Now: clock, Random: random})
	if err != nil {
		return err
	}
	service, err := uipreferences.New(uipreferences.Options{Store: store, Upgrader: upgrader, ConfigDir: func() string { return filepath.Join(input.Home, ".claude") }, Access: boundary.access, Now: clock, Random: random, PID: 42})
	if err != nil {
		return err
	}
	diagnostics := []map[string]string{}
	handler, err := httpapi.NewDesktopUI(httpapi.DesktopUIOptions{Service: service, Authorization: func() access.Authorization { return access.Authorization{PetToken: input.PetToken} }, OnUnhandled: func(event httpapi.UnhandledError) {
		diagnostics = append(diagnostics, map[string]string{"type": event.Type, "severity": event.Severity, "summary": event.Summary})
		fmt.Fprintln(os.Stderr, "[Server] Unexpected error:", event.Error)
		_, _ = os.Stderr.Write(debug.Stack())
	}})
	if err != nil {
		return err
	}
	results := []any{}
	for _, step := range input.Steps {
		data, err := hex.DecodeString(step.Body)
		if err != nil {
			return err
		}
		req := httptest.NewRequest(step.Method, "http://fixture.invalid"+step.Path, bytes.NewReader(data))
		for _, header := range step.Headers {
			req.Header.Add(header[0], strings.Trim(header[1], " \t\r\n"))
		}
		if step.ReadError != nil {
			req.Body = *step.ReadError
		}
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
		results = append(results, map[string]any{"status": response.Code, "headers": headers, "body": hex.EncodeToString(response.Body.Bytes())})
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(map[string]any{"results": results, "trace": boundary.events, "warningCount": len(warnings), "diagnostics": diagnostics})
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
