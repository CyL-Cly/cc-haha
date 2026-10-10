package sandboxruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type ManagerOptions struct {
	Platform, Home, CWD, SessionSuffix                               string
	Environment                                                      map[string]string
	Realpath                                                         func(string) (string, error)
	FindExecutable                                                   func(string) (string, error)
	Network                                                          NetworkOptions
	LinuxTemporaryDirectory, Socat, SeccompBPFPath, ApplySeccompPath string
}
type Manager struct {
	mu              sync.RWMutex
	config          Config
	scrub           []string
	options         ManagerOptions
	network         *Network
	closed          bool
	closeOnce       sync.Once
	closeDone       chan struct{}
	closeError      error
	wraps           sync.WaitGroup
	wrapLifetime    context.Context
	cancelWraps     context.CancelFunc
	lifetimeStop    func() bool
	linuxBridge     *LinuxBridge
	linuxFilesystem *LinuxFilesystem
}

func NewManager(ctx context.Context, converted ConvertedConfig, options ManagerOptions) (*Manager, error) {
	if options.Platform != "darwin" && options.Platform != "macos" && options.Platform != "linux" {
		return nil, fmt.Errorf("sandbox platform %s native runtime is not yet bound", options.Platform)
	}
	if !filepath.IsAbs(options.Home) || !filepath.IsAbs(options.CWD) || options.Realpath == nil || options.FindExecutable == nil || options.SessionSuffix == "" {
		return nil, errors.New("sandbox manager requires real cwd/home/native lookup/session suffix")
	}
	network, e := NewNetwork(ctx, converted.Config.Network, options.Network)
	if e != nil {
		return nil, e
	}
	options.Environment = cloneEnvironment(options.Environment)
	wrapLifetime, cancelWraps := context.WithCancel(ctx)
	manager := &Manager{config: cloneConfig(converted.Config), scrub: cloneSlice(converted.ScrubPaths), options: options, network: network, closeDone: make(chan struct{}), wrapLifetime: wrapLifetime, cancelWraps: cancelWraps}
	if options.Platform == "linux" {
		h, s := network.Ports()
		environment := []string{}
		for key, value := range options.Environment {
			environment = append(environment, key+"="+value)
		}
		bridge, e := NewLinuxBridge(ctx, h, s, LinuxBridgeOptions{TemporaryDirectory: options.LinuxTemporaryDirectory, Socat: options.Socat, Environment: environment})
		if e != nil {
			cancelWraps()
			_ = network.Close(context.Background())
			return nil, e
		}
		manager.linuxBridge = bridge
		rg := converted.Config.Ripgrep.String("command")
		args := []string{}
		_ = json.Unmarshal(converted.Config.Ripgrep.Get("args"), &args)
		manager.linuxFilesystem = &LinuxFilesystem{Paths: PathContext{Home: options.Home, CWD: options.CWD, Realpath: options.Realpath}, TemporaryDirectory: options.LinuxTemporaryDirectory, RipgrepCommand: rg, RipgrepArguments: args, SearchDepth: 3}
	}
	manager.mu.Lock()
	manager.lifetimeStop = context.AfterFunc(ctx, func() { _ = manager.Close(context.Background()) })
	manager.mu.Unlock()
	return manager, nil
}
func (m *Manager) Update(converted ConvertedConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	m.config = cloneConfig(converted.Config)
	m.scrub = append([]string{}, converted.ScrubPaths...)
	m.network.Update(converted.Config.Network)
}
func cloneEnvironment(environment map[string]string) map[string]string {
	if environment == nil {
		return nil
	}
	copy := make(map[string]string, len(environment))
	for key, value := range environment {
		copy[key] = value
	}
	return copy
}
func (m *Manager) UpdateEnvironment(environment map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.closed {
		m.options.Environment = cloneEnvironment(environment)
	}
}
func (m *Manager) ProxyPorts() (float64, float64) { return m.network.Ports() }
func (m *Manager) Wrap(command, shell string) (string, error) {
	return m.WrapContext(context.Background(), command, shell)
}
func (m *Manager) WrapContext(ctx context.Context, command, shell string) (string, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return "", errors.New("Sandbox failed to initialize. ")
	}
	// Admit under the same lock as Close. Slow filesystem discovery runs on
	// immutable per-call settings; a settings refresh must not mutate it.
	m.wraps.Add(1)
	config := cloneConfig(m.config)
	options := m.options
	filesystem := m.linuxFilesystem
	bridge := m.linuxBridge
	lifetime := m.wrapLifetime
	m.mu.Unlock()
	defer m.wraps.Done()
	active, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(lifetime, cancel)
	if lifetime.Err() != nil {
		cancel()
	}
	defer func() { stop(); cancel() }()
	ctx = active
	if filesystem != nil {
		filesystem = filesystem.snapshot(options.CWD, config.Ripgrep, options.Environment)
	}
	read := &ReadRestriction{DenyOnly: []string{}, AllowWithinDeny: []string{}}
	write := &WriteRestriction{AllowOnly: DefaultWritePaths(options.Home), DenyWithinAllow: []string{}}
	for _, path := range config.Filesystem.DenyRead {
		stripped := StripGlobSuffix(path)
		if options.Platform == "linux" && ContainsGlob(stripped) {
			expanded, e := ExpandLinuxGlob(path, filesystem.Paths)
			if e != nil {
				return "", e
			}
			read.DenyOnly = append(read.DenyOnly, expanded...)
		} else {
			read.DenyOnly = append(read.DenyOnly, stripped)
		}
	}
	for _, path := range config.Filesystem.AllowRead {
		stripped := StripGlobSuffix(path)
		if options.Platform == "linux" && ContainsGlob(stripped) {
			expanded, e := ExpandLinuxGlob(path, filesystem.Paths)
			if e != nil {
				return "", e
			}
			read.AllowWithinDeny = append(read.AllowWithinDeny, expanded...)
		} else {
			read.AllowWithinDeny = append(read.AllowWithinDeny, stripped)
		}
	}
	for _, path := range config.Filesystem.AllowWrite {
		stripped := StripGlobSuffix(path)
		if options.Platform != "linux" || !ContainsGlob(stripped) {
			write.AllowOnly = append(write.AllowOnly, stripped)
		}
	}
	for _, path := range config.Filesystem.DenyWrite {
		stripped := StripGlobSuffix(path)
		if options.Platform != "linux" || !ContainsGlob(stripped) {
			write.DenyWithinAllow = append(write.DenyWithinAllow, stripped)
		}
	}
	http, socks := m.network.Ports()
	if options.Platform == "linux" {
		return WrapLinux(ctx, command, LinuxWrapOptions{Filesystem: filesystem, Read: read, Write: write, NeedsNetworkRestriction: true, EnableWeakerNestedSandbox: config.EnableWeakerNestedSandbox != nil && *config.EnableWeakerNestedSandbox, AllowAllUnixSockets: config.Network.AllowAllUnixSockets != nil && *config.Network.AllowAllUnixSockets, HTTPSocketPath: bridge.HTTPSocketPath, SOCKSSocketPath: bridge.SOCKSSocketPath, HTTPProxyPort: &http, SOCKSProxyPort: &socks, Shell: shell, Environment: options.Environment, FindExecutable: options.FindExecutable, SeccompBPFPath: options.SeccompBPFPath, ApplySeccompPath: options.ApplySeccompPath})
	}

	unix := []string{}
	if config.Network.AllowUnixSockets != nil {
		unix = append(unix, (*config.Network.AllowUnixSockets)...)
	}
	macOptions := MacWrapOptions{Environment: options.Environment, Shell: shell, SessionSuffix: options.SessionSuffix, FindExecutable: options.FindExecutable, Profile: MacProfileOptions{Paths: PathContext{Home: options.Home, CWD: options.CWD, Realpath: options.Realpath}, Read: read, Write: write, HTTPProxyPort: &http, SOCKSProxyPort: &socks, NeedsNetworkRestriction: true, AllowUnixSockets: unix, AllowAllUnixSockets: config.Network.AllowAllUnixSockets != nil && *config.Network.AllowAllUnixSockets, AllowLocalBinding: config.Network.AllowLocalBinding != nil && *config.Network.AllowLocalBinding, EnableWeakerNetworkIsolation: config.EnableWeakerNetworkIsolation != nil && *config.EnableWeakerNetworkIsolation}}
	return WrapMac(command, macOptions)
}
func (m *Manager) CleanupAfterCommand() error {
	m.mu.RLock()
	paths := append([]string{}, m.scrub...)
	m.mu.RUnlock()
	if m.linuxFilesystem != nil {
		m.linuxFilesystem.Cleanup()
	}
	// Source records only absent bare-repo candidates at init/config refresh;
	// it removes any attacker-created replacement after the command completes.
	for _, path := range paths {
		if e := os.RemoveAll(path); e != nil && !os.IsNotExist(e) {
			return e
		}
	}
	return nil
}
func (m *Manager) Close(ctx context.Context) error {
	m.closeOnce.Do(func() {
		m.mu.Lock()
		m.closed = true
		if m.lifetimeStop != nil {
			m.lifetimeStop()
		}
		m.cancelWraps()
		m.mu.Unlock()
		go func() {
			// The caller deadline governs its wait only. One resource owner drains
			// network requests before releasing bridges/native helpers.
			m.wraps.Wait()
			m.closeError = m.network.Close(context.Background())
			if m.linuxBridge != nil {
				if e := m.linuxBridge.Close(context.Background()); m.closeError == nil {
					m.closeError = e
				}
			}
			if m.linuxFilesystem != nil {
				m.linuxFilesystem.Cleanup()
			}
			close(m.closeDone)
		}()
	})
	select {
	case <-m.closeDone:
		return m.closeError
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) SetCWD(cwd string) { m.mu.Lock(); m.options.CWD = cwd; m.mu.Unlock() }
func RequiredUnavailableMessage(reason string) string {
	return "\nError: sandbox required but unavailable: " + reason + "\n  sandbox.failIfUnavailable is set — refusing to start without a working sandbox.\n\n"
}
func DisabledUnavailableMessage(reason string) string {
	return "\n⚠ Sandbox disabled: " + reason + "\n  Commands will run WITHOUT sandboxing. Network and filesystem restrictions will NOT be enforced.\n\n"
}
func BareGitMainRepository(cwd string, readFile func(string) ([]byte, error)) string {
	body, e := readFile(filepath.Join(cwd, ".git"))
	if e != nil {
		return ""
	}
	line := ""
	for _, candidate := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(candidate, "gitdir:") {
			line = strings.TrimSpace(strings.TrimPrefix(candidate, "gitdir:"))
			break
		}
	}
	if line == "" {
		return ""
	}
	if !filepath.IsAbs(line) {
		line = filepath.Join(cwd, line)
	}
	marker := string(filepath.Separator) + ".git" + string(filepath.Separator) + "worktrees" + string(filepath.Separator)
	index := strings.LastIndex(line, marker)
	if index > 0 {
		return line[:index]
	}
	return ""
}
