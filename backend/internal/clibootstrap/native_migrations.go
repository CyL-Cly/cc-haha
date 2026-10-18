package clibootstrap

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/analytics"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliglobalconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/climigrations"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelselection"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkpermissions"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
)

type nativeMigrationRuntime struct {
	owner       *cooperative.Owner
	native      *NativeBoundary
	global      *cliglobalconfig.Service
	startup     *clistartupenv.Service
	sources     *clistartupenv.NativeMigrationSources
	refs        ownerasync.NativeReferences
	binding     *ownerasync.NativeBinding
	service     *climigrations.Service
	ignore      *settings.GitIgnore
	cancel      context.CancelFunc
	mu          sync.Mutex
	closed      bool
	tasks       []*climigrations.ChangelogTask
	runScope    *cooperative.Scope
	environment *clistartupenv.Environment
}

type nativeMigrationBindings struct {
	Owner       *cooperative.Owner
	Native      *NativeBoundary
	Global      *cliglobalconfig.Service
	Startup     *clistartupenv.Service
	Settings    *clisettings.Service
	State       *clisettings.State
	Environment *clistartupenv.Environment
	Identity    func(*cooperative.Scope) (modelselection.MetadataIdentity, error)
	Events      *analytics.PublicFacade
	LogError    func(*cooperative.Scope, error) error
}

func newNativeMigrationRuntime(scope *cooperative.Scope, bound nativeMigrationBindings) (*nativeMigrationRuntime, error) {
	if bound.Native == nil || !bound.Native.NativeProcess || scope == nil || !scope.BelongsTo(bound.Owner) || bound.Environment == nil || bound.Events == nil || bound.Identity == nil || bound.LogError == nil {
		return nil, cooperative.ErrRestricted
	}
	sources, err := bound.Startup.BindNativeMigrationSources(scope, bound.Owner, bound.Settings, bound.State, bound.Global)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	runtime := &nativeMigrationRuntime{owner: bound.Owner, native: bound.Native, global: bound.Global, startup: bound.Startup, sources: sources, cancel: cancel, environment: bound.Environment}
	runtime.binding, err = runtime.refs.Bind(bound.Owner)
	if err != nil {
		cancel()
		return nil, err
	}
	environment := func() map[string]string {
		value := map[string]string{}
		for _, property := range bound.Environment.Snapshot().OwnProperties() {
			var key, text string
			if json.Unmarshal(property.NameJSON, &key) == nil && json.Unmarshal(property.Value, &text) == nil {
				value[key] = text
			}
		}
		return value
	}
	runtime.ignore, err = settings.NewGitIgnore(settings.GitIgnoreOptions{HomeDir: bound.Native.Home, Environment: environment, Executable: "git"})
	if err != nil {
		cancel()
		return nil, err
	}
	writer := clisettings.NativeSourceWriterBindings{StartLocalIgnore: func(cwd, filename string) error {
		current := runtime.runScope
		if err := runtime.check(current); err != nil {
			return err
		}
		pending, err := nativeOwnedWork(runtime.owner, runtime.binding, current, func() (struct{}, error) { return struct{}{}, runtime.ignore.AddFileGlobRule(ctx, filename, cwd) })
		if err != nil {
			return err
		}
		_, err = pending.Then(current, func(next *cooperative.Scope, _ struct{}, failure error) error {
			if failure != nil {
				return bound.LogError(next, failure)
			}
			return nil
		})
		return err
	}}
	files := nativeMemoryFiles{n: bound.Native}
	originalCWD := bound.Native.CWD
	runtime.service, err = climigrations.NewNative(scope, climigrations.NativeOptions{Owner: bound.Owner, Binding: runtime.binding, Sources: sources, Global: bound.Global, ConfigHome: bound.Native.ConfigHomeDirectory(), OriginalCWD: bound.Native.CWD, Now: bound.Native.NowMillis, Environment: environment, Identity: bound.Identity,
		ProjectKey: func(next *cooperative.Scope) (string, error) {
			if err := runtime.check(next); err != nil {
				return "", err
			}
			root := usercontext.CanonicalGitRoot(files, files.Realpath, originalCWD)
			if root == "" {
				root = filepath.Clean(originalCWD)
			}
			return filepath.ToSlash(root), nil
		}, Writer: writer,
		AutoModeEnabled: func(next *cooperative.Scope) (string, error) {
			if err := runtime.check(next); err != nil {
				return "", err
			}
			return sdkpermissions.NativeMigrationAutoModeEnabledState(func(name string) string { return environment()[name] }, bound.Global.Get), nil
		}, Event: func(next *cooperative.Scope, name string, metadata protocol.JSONObject) error {
			return bound.Events.Log(next, analytics.Event{Name: name, Metadata: metadata})
		}, LogError: bound.LogError,
		MkdirAll: func(next *cooperative.Scope, path string) (*ownerasync.OwnerFuture[struct{}], error) {
			return nativeOwnedWork(runtime.owner, runtime.binding, next, func() (struct{}, error) { return struct{}{}, files.MkdirAll(path) })
		}, WriteExclusive: func(next *cooperative.Scope, path string, data []byte) (*ownerasync.OwnerFuture[struct{}], error) {
			data = append([]byte(nil), data...)
			return nativeOwnedWork(runtime.owner, runtime.binding, next, func() (struct{}, error) {
				if err := bound.Native.check("migration changelog write", path); err != nil {
					return struct{}{}, err
				}
				file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0666)
				if err != nil {
					return struct{}{}, err
				}
				_, err = file.Write(data)
				closed := file.Close()
				if err == nil {
					err = closed
				}
				return struct{}{}, err
			})
		}})
	if err != nil {
		cancel()
		return nil, err
	}
	return runtime, nil
}

func (runtime *nativeMigrationRuntime) check(scope *cooperative.Scope) error {
	if runtime == nil || scope == nil || !scope.BelongsTo(runtime.owner) || runtime.native == nil || !runtime.native.NativeProcess {
		return cooperative.ErrRestricted
	}
	if err := runtime.sources.Check(scope, runtime.owner, runtime.global); err != nil {
		return err
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.closed {
		return cooperative.ErrClosed
	}
	return nil
}

func (runtime *nativeMigrationRuntime) run(scope *cooperative.Scope, environment *clistartupenv.Environment) error {
	if runtime == nil || environment == nil || runtime.environment != environment {
		return cooperative.ErrRestricted
	}
	if err := runtime.check(scope); err != nil {
		return err
	}
	runtime.runScope = scope
	defer func() { runtime.runScope = nil }()
	task, err := runtime.service.RunNative(scope)
	if err != nil {
		return err
	}
	runtime.mu.Lock()
	runtime.tasks = append(runtime.tasks, task)
	runtime.mu.Unlock()
	if value := runtime.service.DisableAutoUpdater(); value != nil {
		return environment.SetProperty(quoteMain("DISABLE_AUTOUPDATER"), quoteMain(*value))
	}
	return nil
}

// Close joins native obligations with the Owner still alive. A changelog's
// real wx failure is caught by the original migration; cancellation of a held
// Git child is a lifetime action, not an extra migration or model turn.
func (runtime *nativeMigrationRuntime) close() {
	if runtime == nil {
		return
	}
	runtime.mu.Lock()
	if runtime.closed {
		runtime.mu.Unlock()
		return
	}
	runtime.closed = true
	runtime.cancel()
	tasks := append([]*climigrations.ChangelogTask(nil), runtime.tasks...)
	runtime.mu.Unlock()
	for _, task := range tasks {
		_ = task.Wait()
	}
	if count, idle := runtime.refs.Snapshot(); count != 0 {
		<-idle
	}
}

func (m *MainCommand) BeginNativeCommandMigrations(ctx context.Context) error {
	if !m.ownsNativeCommandFactory() || m.commandEvents == nil || m.commandAnalytics == nil {
		return cooperative.ErrRestricted
	}
	return m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		if !m.nativeAPIPreconnect.fired {
			return cooperative.ErrRestricted
		}
		if m.nativeMigrations != nil && (m.nativeMigrations.owner != m.Owner || m.nativeMigrations.native != m.Native || m.nativeMigrations.global != m.Global || m.nativeMigrations.startup != m.Startup) {
			return cooperative.ErrRestricted
		}
		if m.nativeMigrations == nil {
			var err error
			m.nativeMigrations, err = newNativeMigrationRuntime(scope, nativeMigrationBindings{Owner: m.Owner, Native: m.Native, Global: m.Global, Startup: m.Startup, Settings: m.Settings.settings, State: m.Settings.settingsState, Environment: m.Settings.environment, Events: m.commandEvents,
				Identity: func(next *cooperative.Scope) (modelselection.MetadataIdentity, error) {
					_, tokens, _, enabled, err := m.cliAuthIdentity(context.Background(), next, false)
					identity := modelselection.MetadataIdentity{}
					if err != nil {
						return identity, err
					}
					identity.Subscriber, err = openaiauth.ClaudeSubscriber(enabled, tokens)
					if enabled && tokens != nil {
						identity.SubscriptionType = tokens.Object.String("subscriptionType")
						identity.RateLimitTier = tokens.Object.String("rateLimitTier")
					}
					return identity, err
				}, LogError: func(next *cooperative.Scope, failure error) error {
					if !next.BelongsTo(m.Owner) {
						return cooperative.ErrRestricted
					}
					m.commandErrors.Log(failure)
					return nil
				}})
			if err != nil {
				return err
			}
		}
		return m.nativeMigrations.run(scope, m.Settings.environment)
	})
}

func (m *MainPrint) initializeNativeMigrations() error {
	if !m.options.Native.NativeProcess || m.agentChild {
		return nil
	}
	return m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if m.nativeMigrationEvents == nil {
			var err error
			m.nativeMigrationEvents, err = analytics.NewPublicFacade(m.Owner, func(*cooperative.Scope) map[string]string { return m.nativeOpenAIEnvironment() })
			if err != nil {
				return err
			}
		}
		if err := m.initializeNativeErrorLog(); err != nil {
			return err
		}
		if err := m.initializeNativeFileAnalytics(scope); err != nil {
			return err
		}
		var err error
		m.nativeMigrations, err = newNativeMigrationRuntime(scope, nativeMigrationBindings{Owner: m.Owner, Native: m.options.Native, Global: m.Global, Startup: m.Startup, Settings: m.settings, State: m.settingState, Environment: m.environment, Events: m.nativeMigrationEvents,
			Identity: func(next *cooperative.Scope) (modelselection.MetadataIdentity, error) {
				return m.openAIAuth.AnthropicModelIdentity(context.Background(), next, !m.options.Interactive)
			},
			LogError: func(next *cooperative.Scope, failure error) error {
				return m.nativeLogError(context.WithValue(context.Background(), nativeOpenAIAuthScopeKey{}, next), failure)
			}})
		if err != nil {
			return err
		}
		return m.nativeMigrations.run(scope, m.environment)
	})
}
