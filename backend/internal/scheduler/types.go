// Package scheduler ports the desktop scheduled-task store and dispatcher.
// Execution is supplied by the application's real shared Session, not another
// Harness, CLI subprocess or precomputed assistant response.
package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type Error struct {
	Status        int
	Code, Message string
}

func (e *Error) Error() string      { return e.Message }
func bad(message string) error      { return &Error{400, "BAD_REQUEST", message} }
func missing(message string) error  { return &Error{404, "NOT_FOUND", message} }
func internal(message string) error { return &Error{500, "INTERNAL_ERROR", message} }

type CapabilityError struct{ Boundary string }

func (e *CapabilityError) Error() string {
	return "scheduled task capability is not migrated: " + e.Boundary
}

type ExecutionRequest struct {
	Task               protocol.JSONObject
	WorkDir, SessionID string
	Input              protocol.JSONObject
	Environment        map[string]string
}
type ExecutionResult struct {
	ExitCode int
	Stderr   string
}
type Executor func(context.Context, ExecutionRequest, func(json.RawMessage)) (ExecutionResult, error)
type Options struct {
	Home, ConfigDir       string
	Environment           func() map[string]string
	GetProcessEnvironment func(context.Context) (map[string]string, error)
	ProviderRuntimeEnv    func(context.Context, string) (map[string]string, error)
	NetworkEnvironment    func(context.Context, map[string]string) (map[string]string, error)
	AgentTeamsEnabled     func(context.Context) (bool, error)
	OfficialOAuthEnv      func(context.Context) (map[string]string, error)
	Now                   func() time.Time
	RandomBytes           func(int) ([]byte, error)
	Files                 FileSystem
	Execute               Executor
	CreateSession         func(context.Context, string, string) (string, error)
	DeletePlaceholder     func(context.Context, string) error
	AppendSessionMetadata func(context.Context, string, protocol.JSONObject) error
	Notify                func(context.Context, protocol.JSONObject, protocol.JSONObject) error
	GetRawAdapterConfig   func(context.Context) (protocol.JSONObject, error)
	NotificationHTTPDo    func(*http.Request) (*http.Response, error)
	NotificationLog       func(level, message string, details any)
	LogError              func(error)
	RecordDiagnostic      func(context.Context, protocol.JSONObject)
	afterRunWrite         func(string, []byte, []protocol.JSONObject)
}
type FileSystem interface {
	ReadFile(string) ([]byte, error)
	WriteFile(string, []byte, fs.FileMode) error
	MkdirAll(string, fs.FileMode) error
	Rename(string, string) error
	Remove(string) error
	Stat(string) (fs.FileInfo, error)
	Realpath(string) (string, error)
}
type OSFiles struct{}

func (OSFiles) ReadFile(p string) ([]byte, error)                 { return os.ReadFile(p) }
func (OSFiles) WriteFile(p string, b []byte, m fs.FileMode) error { return os.WriteFile(p, b, m) }
func (OSFiles) MkdirAll(p string, m fs.FileMode) error            { return os.MkdirAll(p, m) }
func (OSFiles) Rename(a, b string) error                          { return os.Rename(a, b) }
func (OSFiles) Remove(p string) error                             { return os.Remove(p) }
func (OSFiles) Stat(p string) (fs.FileInfo, error)                { return os.Stat(p) }
func (OSFiles) Realpath(p string) (string, error)                 { return filepath.EvalSymlinks(p) }
func raw(v any) json.RawMessage                                   { b, _ := json.Marshal(v); return b }
func quoted(v string) json.RawMessage                             { return raw(v) }
func malformed(field string) error {
	return fmt.Errorf("scheduled task %s has the original unexpected JSON type", field)
}
