// Package concurrentsessions ports the CLI PID registry onto an existing Owner.
// It owns neither an Agent loop nor a shutdown/forceExit policy.
package concurrentsessions

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"regexp"
	"strconv"
	"syscall"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/runtimecleanup"
)

type VoidFuture = ownerasync.OwnerFuture[struct{}]

type FileSystem interface {
	Mkdir(*cooperative.Scope, string, fs.FileMode) (*VoidFuture, error)
	Chmod(*cooperative.Scope, string, fs.FileMode) (*VoidFuture, error)
	WriteFile(*cooperative.Scope, string, []byte, fs.FileMode) (*VoidFuture, error)
	ReadFile(*cooperative.Scope, string) (*ownerasync.OwnerFuture[[]byte], error)
	ReadDir(*cooperative.Scope, string) (*ownerasync.OwnerFuture[[]string], error)
	Unlink(*cooperative.Scope, string) (*VoidFuture, error)
}

// All live host reads and callbacks execute on the supplied existing Owner.
// The physical FS worker receives only copied paths/modes/bytes. A production
// host must supply its actual bootstrap signal, process probe and debug codec.
type Host struct {
	PID                    int
	BG                     bool
	UDS                    bool
	ConfigHome             func() string
	SessionID              func() string
	OriginalCWD            func() string
	NowMilliseconds        func() int64
	Environment            func(string) (string, bool)
	AgentID                func() *string
	SubscribeSessionSwitch func(*cooperative.Scope, func(*cooperative.Scope, string) error) error
	ProcessRunning         func(float64) bool
	Platform               func() string
	Debug                  func(string)
	ErrorMessage           func(error) string
}

type Service struct {
	owner   *cooperative.Owner
	cleanup *runtimecleanup.Registry
	files   FileSystem
	host    Host
}

func New(owner *cooperative.Owner, cleanup *runtimecleanup.Registry, files FileSystem, host Host) (*Service, error) {
	if owner == nil || cleanup == nil || files == nil || host.PID <= 0 || host.ConfigHome == nil || host.SessionID == nil || host.OriginalCWD == nil || host.NowMilliseconds == nil || host.Environment == nil || host.AgentID == nil || host.SubscribeSessionSwitch == nil || host.ProcessRunning == nil || host.Platform == nil || host.Debug == nil || host.ErrorMessage == nil {
		return nil, cooperative.ErrRestricted
	}
	return &Service{owner: owner, cleanup: cleanup, files: files, host: host}, nil
}

func (service *Service) check(scope *cooperative.Scope) error {
	if service == nil || !scope.BelongsTo(service.owner) {
		return cooperative.ErrRestricted
	}
	return scope.RegisterPromiseReaction(func(*cooperative.Owner) error { return nil })
}

func settle[T any](scope *cooperative.Scope, future *ownerasync.OwnerFuture[T], value T, failure error) error {
	var err error
	if failure != nil {
		err = future.Reject(scope, failure)
	} else {
		err = future.Resolve(scope, value)
	}
	if err != nil {
		_ = future.RejectAdministrative(err)
	}
	return err
}

func observe[T any, R any](scope *cooperative.Scope, source *ownerasync.OwnerFuture[T], result *ownerasync.OwnerFuture[R], run func(*cooperative.Scope, T, error) error) error {
	if source == nil {
		_ = result.RejectAdministrative(cooperative.ErrRestricted)
		return cooperative.ErrRestricted
	}
	_, err := source.Then(scope, run)
	if err != nil {
		_ = result.RejectAdministrative(err)
	}
	return err
}

func administrative(failure error) bool {
	return errors.Is(failure, cooperative.ErrClosed) || errors.Is(failure, cooperative.ErrRestricted)
}

func (service *Service) sessionsDir() string {
	return filepath.Join(service.host.ConfigHome(), "sessions")
}
func (service *Service) pidFile() string {
	return filepath.Join(service.sessionsDir(), strconv.Itoa(service.host.PID)+".json")
}

func (service *Service) kind() string {
	if service.host.BG {
		value, _ := service.host.Environment("CLAUDE_CODE_SESSION_KIND")
		switch value {
		case "bg", "daemon", "daemon-worker":
			return value
		}
	}
	return "interactive"
}

func (service *Service) IsBGSession(scope *cooperative.Scope) (bool, error) {
	if err := service.check(scope); err != nil {
		return false, err
	}
	return service.kind() == "bg", nil
}

func quoted(value string) json.RawMessage {
	return protocol.QuoteJavaScriptUTF16(utf16.Encode([]rune(value)))
}

func (service *Service) registrationData(kind string) ([]byte, error) {
	object := protocol.NewJSONObject(
		protocol.JSONField{Name: "pid", Value: []byte(strconv.Itoa(service.host.PID))},
		protocol.JSONField{Name: "sessionId", Value: quoted(service.host.SessionID())},
		protocol.JSONField{Name: "cwd", Value: quoted(service.host.OriginalCWD())},
		protocol.JSONField{Name: "startedAt", Value: []byte(strconv.FormatInt(service.host.NowMilliseconds(), 10))},
		protocol.JSONField{Name: "kind", Value: quoted(kind)},
	)
	addEnv := func(name, variable string) {
		var raw json.RawMessage
		if value, present := service.host.Environment(variable); present {
			raw = quoted(value)
		}
		object.Set(name, raw)
	}
	addEnv("entrypoint", "CLAUDE_CODE_ENTRYPOINT")
	if service.host.UDS {
		addEnv("messagingSocketPath", "CLAUDE_CODE_MESSAGING_SOCKET")
	}
	if service.host.BG {
		addEnv("name", "CLAUDE_CODE_SESSION_NAME")
		addEnv("logPath", "CLAUDE_CODE_SESSION_LOG")
		addEnv("agent", "CLAUDE_CODE_AGENT")
	}
	return stringify(object)
}

func (service *Service) RegisterSession(scope *cooperative.Scope) (*ownerasync.OwnerFuture[bool], error) {
	if err := service.check(scope); err != nil {
		return nil, err
	}
	result, _ := ownerasync.NewOwnerFuture[bool](service.owner)
	if service.host.AgentID() != nil {
		return result, settle(scope, result, false, nil)
	}
	kind, dir := service.kind(), service.sessionsDir()
	path := filepath.Join(dir, strconv.Itoa(service.host.PID)+".json")
	callback := runtimecleanup.NewCallback(func(next *cooperative.Scope) (*runtimecleanup.Future, error) {
		cleaned, _ := ownerasync.NewOwnerFuture[struct{}](service.owner)
		removed, err := service.files.Unlink(next, path)
		if err != nil {
			_ = cleaned.RejectAdministrative(err)
			return cleaned, err
		}
		err = observe(next, removed, cleaned, func(continuation *cooperative.Scope, _ struct{}, failure error) error {
			if !administrative(failure) {
				failure = nil // Source catches every native unlink rejection.
			}
			return settle(continuation, cleaned, struct{}{}, failure)
		})
		return cleaned, err
	})
	if _, err := service.cleanup.Register(scope, callback); err != nil {
		_ = result.RejectAdministrative(err)
		return result, err
	}
	fail := func(next *cooperative.Scope, failure error) error {
		if administrative(failure) {
			return settle(next, result, false, failure)
		}
		service.host.Debug("[concurrentSessions] register failed: " + service.host.ErrorMessage(failure))
		return settle(next, result, false, nil)
	}
	created, err := service.files.Mkdir(scope, dir, 0700)
	if err != nil {
		_ = result.RejectAdministrative(err)
		return result, err
	}
	err = observe(scope, created, result, func(next *cooperative.Scope, _ struct{}, failure error) error {
		if failure != nil {
			return fail(next, failure)
		}
		changed, err := service.files.Chmod(next, dir, 0700)
		if err != nil {
			_ = result.RejectAdministrative(err)
			return err
		}
		return observe(next, changed, result, func(next *cooperative.Scope, _ struct{}, failure error) error {
			if failure != nil {
				return fail(next, failure)
			}
			// Source reads SID/CWD/clock/env only after the chmod await.
			data, failure := service.registrationData(kind)
			if failure != nil {
				return fail(next, failure)
			}
			written, err := service.files.WriteFile(next, path, data, 0666)
			if err != nil {
				_ = result.RejectAdministrative(err)
				return err
			}
			return observe(next, written, result, func(next *cooperative.Scope, _ struct{}, failure error) error {
				if failure != nil {
					return fail(next, failure)
				}
				if err := service.host.SubscribeSessionSwitch(next, func(next *cooperative.Scope, id string) error {
					_, err := service.updatePIDFile(next, protocol.NewJSONObject(protocol.JSONField{Name: "sessionId", Value: quoted(id)}))
					return err // This future is deliberately not joined.
				}); err != nil {
					_ = result.RejectAdministrative(err)
					return err
				}
				return settle(next, result, true, nil)
			})
		})
	})
	return result, err
}

func stringify(object protocol.JSONObject) ([]byte, error) {
	raw, err := object.MarshalJSON()
	if err != nil {
		return nil, err
	}
	return protocol.ParseAndStringifyJavaScriptJSON(raw)
}

// This is plain JSON.parse + object spread, with no config defaults or BOM
// stripping. Arrays and strings enumerate indexes; primitives have no fields.
func parseSpread(raw []byte) (protocol.JSONObject, error) {
	decoded := protocol.DecodeJavaScriptUTF8(raw)
	if message, invalid := provider.PinnedJavaScriptJSONSyntaxMessage(string(decoded)); invalid {
		units, _ := protocol.DecodeJavaScriptJSONString(message)
		return protocol.JSONObject{}, errors.New(string(utf16.Decode(units)))
	}
	if !json.Valid(decoded) {
		return protocol.JSONObject{}, errors.New("unsupported pinned JSON input")
	}
	raw = bytes.TrimSpace(decoded)
	object := protocol.NewJSONObject()
	switch raw[0] {
	case '{':
		if err := object.UnmarshalJSON(raw); err != nil {
			return object, err
		}
	case '[':
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return object, err
		}
		for index, value := range values {
			object.Set(strconv.Itoa(index), value)
		}
	case '"':
		units, err := protocol.DecodeJavaScriptJSONString(raw)
		if err != nil {
			return object, err
		}
		for index, unit := range units {
			object.Set(strconv.Itoa(index), protocol.QuoteJavaScriptUTF16([]uint16{unit}))
		}
	}
	return object, nil
}

func (service *Service) updatePIDFile(scope *cooperative.Scope, patch protocol.JSONObject) (*VoidFuture, error) {
	if err := service.check(scope); err != nil {
		return nil, err
	}
	result, _ := ownerasync.NewOwnerFuture[struct{}](service.owner)
	path := service.pidFile()
	fail := func(next *cooperative.Scope, failure error) error {
		if administrative(failure) {
			return settle(next, result, struct{}{}, failure)
		}
		service.host.Debug("[concurrentSessions] updatePidFile failed: " + service.host.ErrorMessage(failure))
		return settle(next, result, struct{}{}, nil)
	}
	read, err := service.files.ReadFile(scope, path)
	if err != nil {
		_ = result.RejectAdministrative(err)
		return result, err
	}
	err = observe(scope, read, result, func(next *cooperative.Scope, raw []byte, failure error) error {
		if failure != nil {
			return fail(next, failure)
		}
		object, failure := parseSpread(raw)
		if failure != nil {
			return fail(next, failure)
		}
		object.Assign(patch)
		data, failure := stringify(object)
		if failure != nil {
			return fail(next, failure)
		}
		written, err := service.files.WriteFile(next, path, data, 0666)
		if err != nil {
			_ = result.RejectAdministrative(err)
			return err
		}
		return observe(next, written, result, func(next *cooperative.Scope, _ struct{}, failure error) error {
			if failure != nil {
				return fail(next, failure)
			}
			return settle(next, result, struct{}{}, nil)
		})
	})
	return result, err
}

func (service *Service) publicUpdate(scope *cooperative.Scope, patch protocol.JSONObject, skip bool) (*VoidFuture, error) {
	if err := service.check(scope); err != nil {
		return nil, err
	}
	result, _ := ownerasync.NewOwnerFuture[struct{}](service.owner)
	if skip {
		return result, settle(scope, result, struct{}{}, nil)
	}
	updated, err := service.updatePIDFile(scope, patch)
	if err != nil {
		_ = result.RejectAdministrative(err)
		return result, err
	}
	// The public async function awaits the private async update. This is a
	// separate real continuation, not Promise adoption or a shutdown barrier.
	err = observe(scope, updated, result, func(next *cooperative.Scope, _ struct{}, failure error) error {
		return settle(next, result, struct{}{}, failure)
	})
	return result, err
}

func (service *Service) UpdateSessionName(scope *cooperative.Scope, name *string) (*VoidFuture, error) {
	patch := protocol.NewJSONObject()
	if name != nil {
		patch.Set("name", quoted(*name))
	}
	return service.publicUpdate(scope, patch, name == nil || *name == "")
}

func (service *Service) UpdateSessionBridgeID(scope *cooperative.Scope, id *string) (*VoidFuture, error) {
	value := json.RawMessage("null")
	if id != nil {
		value = quoted(*id)
	}
	return service.publicUpdate(scope, protocol.NewJSONObject(protocol.JSONField{Name: "bridgeSessionId", Value: value}), false)
}

func (service *Service) UpdateSessionActivity(scope *cooperative.Scope, patch protocol.JSONObject) (*VoidFuture, error) {
	if err := service.check(scope); err != nil {
		return nil, err
	}
	if service.host.BG {
		patch = patch.Clone()
		patch.Set("updatedAt", []byte(strconv.FormatInt(service.host.NowMilliseconds(), 10)))
	}
	return service.publicUpdate(scope, patch, !service.host.BG)
}

var pidName = regexp.MustCompile(`^[0-9]+\.json$`)

func inaccessible(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.ENOTDIR) || errors.Is(err, syscall.ELOOP)
}

func (service *Service) CountConcurrentSessions(scope *cooperative.Scope) (*ownerasync.OwnerFuture[int], error) {
	if err := service.check(scope); err != nil {
		return nil, err
	}
	result, _ := ownerasync.NewOwnerFuture[int](service.owner)
	dir := service.sessionsDir()
	listed, err := service.files.ReadDir(scope, dir)
	if err != nil {
		_ = result.RejectAdministrative(err)
		return result, err
	}
	err = observe(scope, listed, result, func(next *cooperative.Scope, names []string, failure error) error {
		if failure != nil {
			if administrative(failure) {
				return settle(next, result, 0, failure)
			}
			if !inaccessible(failure) {
				service.host.Debug("[concurrentSessions] readdir failed: " + service.host.ErrorMessage(failure))
			}
			return settle(next, result, 0, nil)
		}
		count := 0
		for _, name := range names {
			if !pidName.MatchString(name) {
				continue
			}
			pid, _ := strconv.ParseFloat(name[:len(name)-5], 64)
			if pid == float64(service.host.PID) || service.host.ProcessRunning(pid) {
				count++
			} else if service.host.Platform() != "wsl" {
				removed, err := service.files.Unlink(next, filepath.Join(dir, name))
				if err != nil {
					_ = result.RejectAdministrative(err)
					return err
				}
				if _, err := removed.Then(next, func(*cooperative.Scope, struct{}, error) error { return nil }); err != nil {
					_ = result.RejectAdministrative(err)
					return err
				}
			}
		}
		return settle(next, result, count, nil)
	})
	return result, err
}
