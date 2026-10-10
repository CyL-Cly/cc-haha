package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"slices"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type operation struct {
	cancel     context.CancelFunc
	done       chan struct{}
	adapter    Adapter
	startError error
}

type Service struct {
	mu              sync.Mutex
	options         Options
	store           *persistence
	operations      map[string]*operation
	storageFailures map[string]protocol.JSONObject
	closed          bool
}

func New(o Options) (*Service, error) {
	if !filepath.IsAbs(o.Root) || o.Now == nil || o.UUID == nil || o.Bridge == nil || o.CreateAdapter == nil {
		return nil, errors.New("connector service requires an absolute private root, adapter, bridge, clock and UUID")
	}
	p, err := readPersistence(o.Root, o.WriteState)
	if err != nil {
		return nil, err
	}
	return &Service{options: o, store: p, operations: map[string]*operation{}, storageFailures: map[string]protocol.JSONObject{}}, nil
}

func (s *Service) definition(id string) (protocol.JSONObject, error) {
	for _, d := range s.options.Definitions {
		if d.String("id") == id {
			return d.Clone(), nil
		}
	}
	return protocol.JSONObject{}, apiError(404, "Unknown connector")
}

func (s *Service) getLocked(id string) (protocol.JSONObject, error) {
	def, err := s.definition(id)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	state := s.store.get(id)
	if failure, ok := s.storageFailures[id]; ok {
		state.Assign(failure)
	}
	var platforms []string
	_ = json.Unmarshal(def.Get("platforms"), &platforms)
	def.Set("supported", raw(slices.Contains(platforms, s.options.Platform)))
	for _, name := range []string{"installed", "enabled"} {
		v := state.Get(name)
		if v == nil || string(v) == "null" {
			v = raw(false)
		}
		def.Set(name, v)
	}
	def.Set("installedVersion", state.Get("installedVersion"))
	def.Set("updateAvailable", raw(truthy(state.Get("installed")) && !equalStrings(state.Get("installedVersion"), def.Get("version"))))
	for _, pair := range [][2]string{{"connection", "disconnected"}, {"runtime", "missing"}, {"status", "not-installed"}} {
		v := state.Get(pair[0])
		if v == nil || string(v) == "null" {
			v = quote(pair[1])
		}
		def.Set(pair[0], v)
	}
	for _, name := range []string{"operation", "error", "lastCheckedAt", "accountLabel", "verification", "failedPhase"} {
		def.Set(name, state.Get(name))
	}
	return def, nil
}

func (s *Service) Get(id string) (protocol.JSONObject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getLocked(id)
}
func (s *Service) List() ([]protocol.JSONObject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []protocol.JSONObject{}
	for _, def := range s.options.Definitions {
		v, e := s.getLocked(def.String("id"))
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *Service) patchLocked(id string, changes protocol.JSONObject) error {
	if err := s.store.patch(id, changes); err != nil {
		s.storageFailures[id] = record(field("enabled", false), field("runtime", "error"), field("status", "error"), undefined("operation"), field("failedPhase", "persistence"), field("error", "Could not save connector state. Check available disk space and retry."))
		return errors.New("Could not save connector state")
	}
	delete(s.storageFailures, id)
	return nil
}
func (s *Service) patch(id string, v protocol.JSONObject) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.patchLocked(id, v)
}
func (s *Service) saved(id string) protocol.JSONObject {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.get(id)
}

var semver = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?(?:\+[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?$`)
var legacyInstallationVersion = regexp.MustCompile(`^(.+)-(?:darwin|win32)-(?:arm64|x64)$`)

func installedDefinition(def protocol.JSONObject, state protocol.JSONObject) (protocol.JSONObject, error) {
	version := state.Get("installedVersion")
	if nullish(version) {
		installation := nested(state.Get("installation"))
		m := legacyInstallationVersion.FindStringSubmatch(filepath.Base(installation.String("directory")))
		if len(m) > 1 {
			version = quote(m[1])
		}
	}
	if nullish(version) {
		version = def.Get("version")
	}
	coerced, err := protocol.JavaScriptStringJSON(version)
	var text string
	if err != nil || json.Unmarshal(coerced, &text) != nil || !semver.MatchString(text) {
		return protocol.JSONObject{}, errors.New("Invalid installed connector version")
	}
	def = def.Clone()
	def.Set("version", version)
	return def, nil
}

func (s *Service) Action(id, action string, opts ActionOptions) (protocol.JSONObject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return protocol.JSONObject{}, context.Canceled
	}
	def, err := s.definition(id)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	current, err := s.getLocked(id)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if action == "cancel" {
		op := s.operations[id]
		if op == nil {
			return protocol.JSONObject{}, apiError(409, "No operation to cancel")
		}
		op.cancel()
		v := nested(current.Get("operation"))
		v.Set("phase", quote("cancelling"))
		v.Set("authUrl", nil)
		if err = s.patchLocked(id, record(field("operation", v))); err != nil {
			return protocol.JSONObject{}, err
		}
		return s.getLocked(id)
	}
	if !slices.Contains([]string{"prepare", "authenticate", "check", "deactivate", "remove"}, action) {
		return protocol.JSONObject{}, apiError(404, "Unknown connector endpoint")
	}
	if s.operations[id] != nil {
		return protocol.JSONObject{}, apiError(409, "A connector operation is already running")
	}
	if !truthy(current.Get("supported")) && action != "remove" && action != "deactivate" {
		return protocol.JSONObject{}, apiError(400, "Connector is not supported on this platform")
	}
	previous := s.store.get(id)
	if (action == "authenticate" || action == "check" && !truthy(previous.Get("sharedCredentialsAcknowledged"))) && def.String("credentialMode") == "shared" && !opts.AcknowledgeSharedCredentials {
		return protocol.JSONObject{}, apiError(400, "Confirm shared credentials before connecting")
	}
	if action == "prepare" && truthy(current.Get("installed")) && !truthy(current.Get("updateAvailable")) && current.String("status") != "error" {
		return protocol.JSONObject{}, apiError(409, "Connector is already installed")
	}
	if (action == "authenticate" || action == "check") && !truthy(previous.Get("installation")) {
		return protocol.JSONObject{}, apiError(409, "Prepare this connector first")
	}
	if opts.Configuration != nil && !slices.Contains([]string{"prepare", "authenticate"}, action) {
		return protocol.JSONObject{}, apiError(400, "Configuration is only accepted during setup or authentication")
	}
	if action == "prepare" && truthy(current.Get("installed")) {
		for _, v := range opts.Configuration {
			if trimJS(v) != "" {
				return protocol.JSONObject{}, apiError(400, "Use authentication to change credentials on an installed connector")
			}
		}
	}
	changes := record(field("operation", record(field("id", s.options.UUID()), field("kind", action), field("phase", action), field("startedAt", s.options.Now().UTC().Format("2006-01-02T15:04:05.000Z")))), undefined("error"), undefined("failedPhase"))
	if action == "prepare" {
		changes.Set("status", quote("preparing"))
	}
	if action == "authenticate" {
		changes.Set("status", quote("authorizing"))
	}
	if err = s.patchLocked(id, changes); err != nil {
		return protocol.JSONObject{}, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	op := &operation{cancel: cancel, done: make(chan struct{})}
	s.operations[id] = op
	selected := def
	if action != "prepare" {
		selected, op.startError = installedDefinition(def, previous)
	}
	if op.startError == nil {
		op.adapter, op.startError = s.options.CreateAdapter(selected, s.options.Root)
	}
	// The original async run detaches old runtime publication before its first
	// bridge await, after adapter construction succeeds. Preserve the same
	// visible state without detaching on an earlier constructor failure.
	if op.startError == nil && (action == "authenticate" || action == "check") {
		status := "needs-auth"
		if action == "authenticate" {
			status = "authorizing"
		}
		op.startError = s.patchLocked(id, record(field("runtime", "missing"), field("connection", "needs-auth"), field("enabled", false), undefined("verification"), undefined("accountLabel"), field("status", status)))
	}
	result, err := s.getLocked(id)
	go s.run(ctx, def, action, opts, previous, op)
	return result, err
}

func installationFrom(value json.RawMessage) (Installation, error) {
	var i Installation
	err := json.Unmarshal(value, &i)
	return i, err
}
func (s *Service) progress(id string, ctx context.Context, op *operation) Progress {
	return func(phase string, url *string) {
		if !slices.Contains([]string{"downloading", "extracting", "verifying", "awaiting-authorization", "configuring-account", "authorizing", "installing-plugin", "checking", "enabling-plugin", "refreshing-sessions", "verifying-runtime"}, phase) || ctx.Err() != nil {
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if ctx.Err() != nil || s.operations[id] != op {
			return
		}
		v, _ := s.getLocked(id)
		state := nested(v.Get("operation"))
		state.Set("phase", quote(phase))
		state.Set("authUrl", nil)
		if url != nil {
			state.Set("authUrl", quote(*url))
		}
		if s.patchLocked(id, record(field("operation", state))) != nil {
			op.cancel()
		}
	}
}

func (s *Service) run(ctx context.Context, def protocol.JSONObject, action string, opts ActionOptions, previous protocol.JSONObject, op *operation) {
	id := def.String("id")
	bridge := s.options.Bridge
	progress := s.progress(id, ctx, op)
	published := false
	adapter := op.adapter
	work := func() error {
		if op.startError != nil {
			return op.startError
		}
		var err error
		active := func() error { return ctx.Err() }
		if action == "authenticate" || action == "check" {
			if err = bridge.SetEnabled(ctx, def, false); err != nil {
				return err
			}
			if err = bridge.Reload(ctx, opts.SessionID, nil); err != nil {
				return err
			}
			if err = active(); err != nil {
				return err
			}
		}
		if len(opts.Configuration) > 0 {
			configurable, ok := adapter.(ConfigurableAdapter)
			if !ok {
				return errors.New("Connector does not accept configuration for this action")
			}
			if err = configurable.Configure(ctx, opts.Configuration); err != nil {
				return err
			}
			if err = active(); err != nil {
				return err
			}
		}
		if action == "prepare" {
			installation, err := adapter.Prepare(ctx, progress)
			if err != nil {
				return err
			}
			if err = active(); err != nil {
				return err
			}
			progress("installing-plugin", nil)
			if err = active(); err != nil {
				return err
			}
			if err = bridge.Install(ctx, def, installation); err != nil {
				return err
			}
			published = true
			if err = bridge.SetEnabled(ctx, def, false); err != nil {
				return err
			}
			if err = active(); err != nil {
				return err
			}
			if err = bridge.Reload(ctx, opts.SessionID, nil); err != nil {
				return err
			}
			if err = active(); err != nil {
				return err
			}
			ack := previous.Get("sharedCredentialsAcknowledged")
			if ack == nil || string(ack) == "null" {
				ack = raw(false)
			}
			if err = s.patch(id, record(field("installation", installation), field("installed", true), field("installedVersion", def.String("version")), protocol.JSONField{Name: "sharedCredentialsAcknowledged", Value: ack}, field("enabled", false), field("connection", "needs-auth"), field("runtime", "missing"), field("status", "needs-auth"), undefined("verification"), undefined("lastCheckedAt"), undefined("accountLabel"))); err != nil {
				return err
			}
			if def.String("transport") == "skills" {
				checked, err := adapter.Check(ctx, installation)
				if err != nil {
					return err
				}
				if err = active(); err != nil {
					return err
				}
				if !checked.Authenticated {
					return errors.New("Skill package validation failed")
				}
				progress("enabling-plugin", nil)
				if err = bridge.SetEnabled(ctx, def, true); err != nil {
					return err
				}
				if err = active(); err != nil {
					return err
				}
				if err = bridge.Reload(ctx, opts.SessionID, &def); err != nil {
					return err
				}
				if err = active(); err != nil {
					return err
				}
				ready, err := bridge.Ready(ctx, def)
				if err != nil {
					return err
				}
				if !ready {
					return errors.New("Skill package could not load")
				}
				if err = active(); err != nil {
					return err
				}
				return s.patch(id, record(field("enabled", true), field("connection", "connected"), field("runtime", "ready"), field("status", "configured"), field("verification", "local")))
			}
			return nil
		}
		if action == "authenticate" || action == "check" {
			installation, err := installationFrom(s.saved(id).Get("installation"))
			if err != nil {
				return err
			}
			progress("checking", nil)
			if err = active(); err != nil {
				return err
			}
			checked, err := adapter.Check(ctx, installation)
			if err != nil {
				return err
			}
			if err = active(); err != nil {
				return err
			}
			if action == "authenticate" && !checked.Authenticated {
				progress("authorizing", nil)
				if err = active(); err != nil {
					return err
				}
				if err = adapter.Authenticate(ctx, installation, progress); err != nil {
					return err
				}
				if err = active(); err != nil {
					return err
				}
				progress("checking", nil)
				if err = active(); err != nil {
					return err
				}
				checked, err = adapter.Check(ctx, installation)
				if err != nil {
					return err
				}
				if err = active(); err != nil {
					return err
				}
			}
			changes := record(field("lastCheckedAt", s.options.Now().UTC().Format("2006-01-02T15:04:05.000Z")), undefined("accountLabel"), undefined("verification"))
			if checked.AccountLabelJSON != nil {
				changes.Set("accountLabel", checked.AccountLabelJSON)
			} else if checked.AccountLabel != nil {
				changes.Set("accountLabel", quote(*checked.AccountLabel))
			}
			if checked.Verification != "" {
				changes.Set("verification", quote(checked.Verification))
			}
			if err = s.patch(id, changes); err != nil {
				return err
			}
			if !checked.Authenticated {
				return s.patch(id, record(field("connection", "needs-auth"), field("runtime", "missing"), field("enabled", false), field("status", "needs-auth")))
			}
			changes = record(field("connection", "connected"), undefined("sharedCredentialsAcknowledged"))
			if def.String("credentialMode") == "shared" {
				changes.Set("sharedCredentialsAcknowledged", raw(true))
			}
			if err = s.patch(id, changes); err != nil {
				return err
			}
			progress("enabling-plugin", nil)
			if err = active(); err != nil {
				return err
			}
			if err = bridge.SetEnabled(ctx, def, true); err != nil {
				return err
			}
			if err = active(); err != nil {
				return err
			}
			progress("refreshing-sessions", nil)
			if err = active(); err != nil {
				return err
			}
			if err = bridge.Reload(ctx, opts.SessionID, &def); err != nil {
				return err
			}
			if err = active(); err != nil {
				return err
			}
			progress("verifying-runtime", nil)
			if err = active(); err != nil {
				return err
			}
			ready, err := bridge.Ready(ctx, def)
			if err != nil {
				return err
			}
			if !ready {
				return errors.New("Connector runtime is not ready")
			}
			if err = active(); err != nil {
				return err
			}
			status := "configured"
			if checked.Verification == "remote" {
				status = "ready"
			}
			return s.patch(id, record(field("enabled", true), field("runtime", "ready"), field("status", status)))
		}
		if err = bridge.SetEnabled(ctx, def, false); err != nil {
			return err
		}
		if err = s.patch(id, record(field("enabled", false), field("runtime", "missing"), field("status", "disabled"))); err != nil {
			return err
		}
		if err = bridge.Reload(ctx, opts.SessionID, nil); err != nil {
			return err
		}
		if err = active(); err != nil {
			return err
		}
		if err = adapter.Deactivate(ctx); err != nil {
			return err
		}
		if action == "remove" {
			if err = bridge.Remove(ctx, def); err != nil {
				return err
			}
			if installation := s.saved(id).Get("installation"); installation != nil {
				value, err := installationFrom(installation)
				if err != nil {
					return err
				}
				if err = adapter.Remove(ctx, value); err != nil {
					return err
				}
			}
			return s.patch(id, record(field("installed", false), undefined("installedVersion"), undefined("installation"), field("sharedCredentialsAcknowledged", false), field("connection", "disconnected"), field("status", "not-installed"), undefined("accountLabel"), undefined("lastCheckedAt")))
		}
		return nil
	}
	err := work()
	var failureError error
	if err != nil {
		failureError = s.handleFailure(ctx, def, action, opts, previous, adapter, published)
	}
	if err = s.patch(id, record(undefined("operation"))); err != nil || failureError != nil {
		if bridge.SetEnabled(context.Background(), def, false) == nil {
			_ = bridge.Reload(context.Background(), opts.SessionID, nil)
		}
	}
	s.mu.Lock()
	delete(s.operations, id)
	s.mu.Unlock()
	op.cancel()
	close(op.done)
}

func (s *Service) handleFailure(ctx context.Context, def protocol.JSONObject, action string, opts ActionOptions, previous protocol.JSONObject, adapter Adapter, published bool) error {
	id := def.String("id")
	bridge := s.options.Bridge
	cleanup := context.Background()
	cancelled := ctx.Err() != nil
	s.mu.Lock()
	_, persistenceFailed := s.storageFailures[id]
	current, _ := s.getLocked(id)
	s.mu.Unlock()
	phase := nested(current.Get("operation")).String("phase")
	if phase == "" {
		phase = action
	}
	if persistenceFailed {
		phase = "persistence"
	}
	rollback := action == "prepare" && truthy(previous.Get("installed")) && truthy(previous.Get("installation"))
	restored := false
	if rollback {
		old, err := installedDefinition(def, previous)
		installation, e := installationFrom(previous.Get("installation"))
		if err == nil && e == nil {
			err = bridge.Install(cleanup, old, installation)
			if err == nil {
				err = bridge.SetEnabled(cleanup, old, truthy(previous.Get("enabled")))
			}
			if err == nil {
				var required *protocol.JSONObject
				if truthy(previous.Get("enabled")) {
					required = &old
				}
				err = bridge.Reload(cleanup, opts.SessionID, required)
			}
			restored = err == nil
		}
	}
	cleanupFailed := false
	if !restored {
		var cleanupError error
		if published || truthy(previous.Get("installation")) {
			cleanupError = bridge.SetEnabled(cleanup, def, false)
		}
		if cleanupError == nil {
			cleanupError = bridge.Reload(cleanup, opts.SessionID, nil)
		}
		cleanupFailed = cleanupError != nil
	}
	if cancelled && !restored && adapter != nil {
		if adapter.Deactivate(cleanup) != nil {
			cleanupFailed = true
		}
	}
	changes := protocol.NewJSONObject()
	if cancelled && !persistenceFailed && !cleanupFailed {
		if action == "prepare" && (restored || !rollback && !published) {
			changes = previous.Clone()
			if !restored {
				changes.Set("enabled", raw(false))
				changes.Set("runtime", quote("missing"))
				status, connection := "not-installed", "disconnected"
				if truthy(previous.Get("installed")) {
					status, connection = "disabled", "needs-auth"
				}
				changes.Set("status", quote(status))
				changes.Set("connection", quote(connection))
			}
		}
		if action == "authenticate" || action == "check" {
			changes = record(field("connection", "needs-auth"), field("runtime", "missing"), field("enabled", false), field("status", "needs-auth"), undefined("verification"), undefined("accountLabel"), undefined("error"), undefined("failedPhase"))
		}
		if !changes.IsObject() || len(changes.OwnProperties()) == 0 {
			changes = record(field("enabled", false), field("runtime", "error"), field("status", "error"), field("failedPhase", phase), field("error", "Operation cancelled. Check the connector before using it."))
		}
	} else {
		if action == "prepare" {
			changes = previous.Clone()
		}
		enabled := false
		if restored {
			enabled = truthy(previous.Get("enabled"))
		}
		message := "Connector setup failed. Retry or check the connection."
		if cancelled {
			message = "Operation cancelled. Check the connector before using it."
		}
		changes.Assign(record(field("enabled", enabled), field("runtime", "error"), field("status", "error"), field("failedPhase", phase), field("error", message)))
	}
	return s.patch(id, changes)
}

func (s *Service) Wait(ctx context.Context, id string) error {
	s.mu.Lock()
	op := s.operations[id]
	s.mu.Unlock()
	if op == nil {
		return nil
	}
	select {
	case <-op.done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
func (s *Service) Close(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	ops := []*operation{}
	for _, op := range s.operations {
		op.cancel()
		ops = append(ops, op)
	}
	s.mu.Unlock()
	for _, op := range ops {
		select {
		case <-op.done:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	return nil
}
