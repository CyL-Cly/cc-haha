// Package computeruse owns the original desktop configuration and native
// Computer Use transport. The Swift helper remains the OS implementation.
package computeruse

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/diagnosticsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

type CommandResult struct {
	Stdout, Stderr string
	Code           int
}
type Runner func(context.Context, string, []string, []*os.File) (CommandResult, error)
type Options struct {
	HomeDir, ConfigDir, AppRoot, ProjectRoot, WorkDir, TempDir, Executable string
	Environment                                                            map[string]string
	Platform, Arch                                                         string
	Store                                                                  *storage.Store
	Diagnostics                                                            *diagnosticsapi.Service
	Run                                                                    Runner
	AppRoots                                                               []string
	RequestTimeout, ReadinessTimeout                                       time.Duration
}
type Service struct {
	options        Options
	ctx            context.Context
	cancel         context.CancelFunc
	mu             sync.Mutex
	clients        map[string]*daemonClient
	windowsClients map[string]*windowsClient
	closed         bool
	operations     sync.WaitGroup
	nextGeneration uint64
	sourceResolved bool
	sourceBinary   string
	appsMu         sync.Mutex
	appsAt         time.Time
	apps           []InstalledApp
	icons          map[string][]byte
	iconKeys       []string
}

func New(options Options) (*Service, error) {
	if !filepath.IsAbs(options.HomeDir) || !filepath.IsAbs(options.ConfigDir) || !filepath.IsAbs(options.WorkDir) {
		return nil, errors.New("Computer Use requires explicit absolute home/config/work paths")
	}
	if options.Platform == "" {
		options.Platform = runtime.GOOS
	}
	if options.Platform == "windows" {
		options.Platform = "win32"
	}
	if options.Arch == "" {
		options.Arch = runtime.GOARCH
	}
	if options.Arch == "amd64" {
		options.Arch = "x64"
	}
	environment := map[string]string{}
	for key, value := range options.Environment {
		environment[key] = value
	}
	if options.Platform == "win32" {
		environment["PYTHONIOENCODING"] = "utf-8"
		environment["PYTHONUTF8"] = "1"
	}
	options.Environment = environment
	if options.TempDir == "" {
		options.TempDir = environment["TMPDIR"]
	}
	if options.TempDir == "" {
		options.TempDir = os.TempDir()
	}
	if options.Executable == "" {
		options.Executable, _ = os.Executable()
	}
	if options.ProjectRoot == "" {
		options.ProjectRoot = options.AppRoot
		if filepath.Base(options.ProjectRoot) == "desktop" {
			options.ProjectRoot = filepath.Dir(options.ProjectRoot)
		}
	}
	if options.Store == nil {
		options.Store = storage.New(storage.Options{})
	}
	if options.Diagnostics == nil {
		var err error
		options.Diagnostics, err = diagnosticsapi.New(diagnosticsapi.Options{HomeDir: options.HomeDir, ConfigDir: options.ConfigDir})
		if err != nil {
			return nil, err
		}
	}
	if options.Run == nil {
		options.Run = nativeRunner(options)
	}
	if options.RequestTimeout == 0 {
		options.RequestTimeout = 20 * time.Second
	}
	if options.ReadinessTimeout == 0 {
		options.ReadinessTimeout = 8 * time.Second
	}
	if options.AppRoots == nil {
		options.AppRoots = []string{"/Applications", filepath.Join(options.HomeDir, "Applications"), "/System/Applications"}
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{options: options, ctx: ctx, cancel: cancel, clients: map[string]*daemonClient{}, windowsClients: map[string]*windowsClient{}, icons: map[string][]byte{}}, nil
}
func randomID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(err)
	}
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80
	text := hex.EncodeToString(raw[:])
	return text[:8] + "-" + text[8:12] + "-" + text[12:16] + "-" + text[16:20] + "-" + text[20:]
}

type CapabilityError struct{ Capability string }

func (err *CapabilityError) Error() string {
	return "Go Computer Use capability is not implemented: " + err.Capability
}

// Call rereads the shared enablement before every operation. It never treats
// the legacy authorizedApps list as an authorization boundary.
func (service *Service) Call(ctx context.Context, sessionID, command string, payload json.RawMessage) (json.RawMessage, error) {
	ctx, cancel := service.ownedContext(ctx)
	defer cancel()
	config, err := service.Config(ctx)
	if err != nil {
		return nil, err
	}
	enabled, _ := boolean(config["enabled"])
	flag := strings.ToLower(trim(service.options.Environment["CLAUDE_COMPUTER_USE_ENABLED"]))
	if !enabled || flag == "0" || flag == "false" || flag == "no" || flag == "off" {
		return nil, errors.New("Computer Use is disabled in Settings")
	}
	if service.options.Platform != "darwin" && service.options.Platform != "win32" {
		return nil, &CapabilityError{"Windows compatibility helper transport"}
	}
	if sessionID == "" {
		return nil, errors.New("Computer Use requires the actual session ID")
	}
	if _, ok := decodeObject(payload); !ok {
		if len(payload) == 0 {
			payload = json.RawMessage(`{}`)
		} else {
			return nil, errors.New("Computer Use payload must be an object")
		}
	}
	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		return nil, errors.New("Computer Use service is closed")
	}
	if service.options.Platform == "win32" {
		client := service.windowsClients[sessionID]
		if client == nil {
			client = newWindowsClient(service)
			service.windowsClients[sessionID] = client
		}
		service.mu.Unlock()
		return client.Call(ctx, command, payload)
	}
	client := service.clients[sessionID]
	if client == nil {
		client = newDaemonClient(service, sessionID)
		service.clients[sessionID] = client
	}
	service.mu.Unlock()
	return client.Call(ctx, command, payload)
}
func (service *Service) EndTurn(ctx context.Context, sessionID string) error {
	service.mu.Lock()
	client := service.clients[sessionID]
	windows := service.windowsClients[sessionID]
	service.mu.Unlock()
	if windows != nil {
		return windows.EndTurn(ctx)
	}
	if client == nil {
		return nil
	}
	return client.EndTurn(ctx)
}

// CloseSession releases the native process owned by a retired application
// session. A later factory for that same persisted SID gets a fresh daemon.
func (service *Service) CloseSession(ctx context.Context, sessionID string) error {
	service.mu.Lock()
	client := service.clients[sessionID]
	windows := service.windowsClients[sessionID]
	service.mu.Unlock()
	if windows != nil {
		err := windows.Close(ctx)
		service.mu.Lock()
		if service.windowsClients[sessionID] == windows {
			delete(service.windowsClients, sessionID)
		}
		service.mu.Unlock()
		return err
	}
	if client == nil {
		return nil
	}
	err := client.Close(ctx)
	service.mu.Lock()
	if service.clients[sessionID] == client {
		delete(service.clients, sessionID)
	}
	service.mu.Unlock()
	return err
}
func (service *Service) Close(ctx context.Context) error {
	service.mu.Lock()
	service.closed = true
	clients := make([]*daemonClient, 0, len(service.clients))
	for _, client := range service.clients {
		clients = append(clients, client)
	}
	windows := make([]*windowsClient, 0, len(service.windowsClients))
	for _, client := range service.windowsClients {
		windows = append(windows, client)
	}
	service.mu.Unlock()
	service.cancel()
	var failures []error
	for _, client := range windows {
		if err := client.Close(ctx); err != nil {
			failures = append(failures, err)
		}
	}
	for _, client := range clients {
		if err := client.Close(ctx); err != nil {
			failures = append(failures, err)
		}
	}
	joined := make(chan struct{})
	go func() { service.operations.Wait(); close(joined) }()
	select {
	case <-joined:
	case <-ctx.Done():
		failures = append(failures, ctx.Err())
	}
	return errors.Join(failures...)
}

func (service *Service) ownedContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(service.ctx, cancel)
	return ctx, func() { stop(); cancel() }
}
