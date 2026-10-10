package localindex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/activitystatsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

type Configuration struct {
	Mode, Scope, DatabasePath string
	WarningCode               *string
}
type Status struct {
	Mode            string  `json:"mode"`
	State           string  `json:"state"`
	Discovered      int64   `json:"discovered"`
	Indexed         int64   `json:"indexed"`
	DegradedSources int64   `json:"degradedSources"`
	DatabaseBytes   int64   `json:"databaseBytes"`
	WALBytes        int64   `json:"walBytes"`
	LastUpdatedAt   *string `json:"lastUpdatedAt"`
	LastErrorCode   *string `json:"lastErrorCode"`
}
type SchedulingMetrics struct {
	MaxBatchSize int `json:"maxBatchSize"`
	YieldCount   int `json:"yieldCount"`
}
type CoordinatorOptions struct {
	Scope, Mode, DatabasePath string
	ShotStatsEnabled          bool
	ResolveConfiguration      func() Configuration
	Budget                    *sessionstore.Budget
	Reduction                 ReductionOptions
	SourceMetadata            func(string) (SourceMetadata, error)
	Now                       func() time.Time
	WatcherOptions            WatchOptions
	StorageLimitBytes         int64
	BusyCooldown              time.Duration
	OpenDatabase              func(context.Context, DatabaseOptions) (*Database, error)
	BeforeProject             func(context.Context, SessionCandidate) error
	Backup                    func(BackupOptions) (BackupResult, error)
}
type primaryGeneration struct {
	configuration Configuration
	ctx           context.Context
	cancel        context.CancelFunc
	db            *Database
	index         *SessionIndex
	activity      *ActivityIndex
	projector     *SessionProjector
	watcher       *Watcher
	initialDone   chan struct{}
}
type rebuildCall struct {
	done   chan struct{}
	status Status
	err    error
}

type Coordinator struct {
	consumerCooldownUntil                                                   time.Time
	rebuildMu                                                               sync.Mutex
	pendingRebuild                                                          *rebuildCall
	epoch, mutationEpoch, coveredMutationEpoch                              uint64
	o                                                                       CoordinatorOptions
	mu                                                                      sync.Mutex
	lifecycleMu                                                             sync.Mutex
	queueMu                                                                 sync.Mutex
	active                                                                  *primaryGeneration
	status                                                                  Status
	failed                                                                  map[string]string
	failedOrder                                                             []string
	sweepFailures                                                           map[string]string
	fullFailure, indexFailure                                               string
	previousFailure, servable, watchHealthy, storageLimited, checkpointBusy bool
	cooldownUntil                                                           time.Time
	scheduling                                                              SchedulingMetrics
	reconfiguring                                                           bool
}

func NewCoordinator(o CoordinatorOptions) (*Coordinator, error) {
	if !filepath.IsAbs(o.Scope) || o.Budget == nil || o.SourceMetadata == nil || o.Reduction.ParseDate == nil || o.Reduction.Compare == nil {
		return nil, errors.New("Primary coordinator requires its explicit config scope, shared history budget and real metadata/date/collation adapters")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.OpenDatabase == nil {
		o.OpenDatabase = OpenDatabase
	}
	if o.Backup == nil {
		o.Backup = BackupDatabaseFamily
	}
	if o.StorageLimitBytes <= 0 {
		o.StorageLimitBytes = ContentStorageLimit
	}
	if o.BusyCooldown <= 0 {
		o.BusyCooldown = 5 * time.Second
	}
	if o.Reduction.Location == nil {
		o.Reduction.Location = time.Local
	}
	return &Coordinator{o: o, status: Status{Mode: "off", State: "off"}, failed: map[string]string{}, watchHealthy: true}, nil
}
func (c *Coordinator) configuration() Configuration {
	config := Configuration{Mode: c.o.Mode, Scope: c.o.Scope, DatabasePath: c.o.DatabasePath}
	if c.o.ResolveConfiguration != nil {
		config = c.o.ResolveConfiguration()
	}
	if config.Mode == "" {
		config.Mode = "on"
	}
	if config.Mode != "on" && config.Mode != "shadow" && config.Mode != "off" {
		config.Mode = "on"
		config.WarningCode = stringPointer("LOCAL_INDEX_INVALID_MODE")
	}
	if config.DatabasePath == "" {
		config.DatabasePath = filepath.Join(config.Scope, "cc-haha", "db", filename(Primary))
	}
	return config
}
func sameConfiguration(a, b Configuration) bool {
	return a.Mode == b.Mode && (a.Mode == "off" || a.Scope == b.Scope && a.DatabasePath == b.DatabasePath)
}
func (c *Coordinator) valid(g *primaryGeneration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.active == g && g.ctx.Err() == nil
}
func (c *Coordinator) update(g *primaryGeneration, f func()) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active != g || g.ctx.Err() != nil {
		return false
	}
	f()
	return true
}
func (c *Coordinator) stamp() *string {
	value := c.o.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	return &value
}
func (c *Coordinator) markDegraded(g *primaryGeneration, err error, fallback string) {
	code := errorCode(err, fallback)
	c.update(g, func() {
		c.indexFailure = code
		c.status.State = "degraded"
		c.status.LastErrorCode = stringPointer(code)
		c.status.LastUpdatedAt = c.stamp()
		if code == "SQLITE_BUSY" {
			c.cooldownUntil = c.o.Now().Add(c.o.BusyCooldown)
		}
	})
}
func (c *Coordinator) Start(ctx context.Context) error {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	config := c.configuration()
	c.mu.Lock()
	g := c.active
	c.mu.Unlock()
	if g != nil && sameConfiguration(g.configuration, config) {
		return nil
	}
	if g != nil {
		c.stopLocked()
	}
	return c.startLocked(ctx, config)
}
func (c *Coordinator) startLocked(ctx context.Context, config Configuration) error {
	c.mu.Lock()
	c.status = Status{Mode: config.Mode, State: "building"}
	if config.Mode == "off" {
		c.status.State = "off"
		c.status.LastErrorCode = config.WarningCode
	}
	c.mu.Unlock()
	if config.Mode == "off" {
		return nil
	}
	if !filepath.IsAbs(config.Scope) || !filepath.IsAbs(config.DatabasePath) {
		return c.startFailure(unsafePath())
	}
	db, e := c.o.OpenDatabase(ctx, DatabaseOptions{config.DatabasePath, config.Scope, Primary})
	if confirmedCorruption(e) {
		_, backupErr := c.o.Backup(BackupOptions{Scope: config.Scope, DatabasePath: config.DatabasePath, Reason: "SQLITE_CORRUPT", Now: c.o.Now})
		if backupErr != nil {
			return c.startFailure(backupErr)
		}
		db, e = c.o.OpenDatabase(ctx, DatabaseOptions{config.DatabasePath, config.Scope, Primary})
	}
	if e != nil {
		return c.startFailure(e)
	}
	index, _ := NewSessionIndex(db)
	activity, _ := NewActivityIndex(db, c.o.Reduction.Location, c.o.ShotStatsEnabled)
	persisted, e := index.GetBackfill(ctx, config.Scope)
	if e != nil {
		_ = db.Close()
		return c.startFailure(e)
	}
	sources, e := index.ListSources(ctx)
	if e != nil {
		_ = db.Close()
		return c.startFailure(e)
	}
	activityBackfill, e := activity.GetBackfill(ctx, config.Scope)
	if e != nil {
		_ = db.Close()
		return c.startFailure(e)
	}
	if activityBackfill != nil && activityBackfill.State == "ready" {
		activitySources, e := activity.ListSources(ctx)
		if e != nil {
			_ = db.Close()
			return c.startFailure(e)
		}
		for _, source := range activitySources {
			if source.ParserVersion != SessionParserVersion {
				e = db.Transaction(ctx, func(op *Operation) error { return MarkActivityBuilding(op, config.Scope, c.o.Now().UnixMilli()) })
				if e != nil {
					_ = db.Close()
					return c.startFailure(e)
				}
				break
			}
		}
	}
	ownedCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	g := &primaryGeneration{configuration: config, ctx: ownedCtx, cancel: cancel, db: db, index: index, activity: activity, initialDone: make(chan struct{})}
	projector, e := NewSessionProjector(SessionProjectorOptions{Database: db, Index: index, Activity: activity, Scope: config.Scope, Budget: c.o.Budget, Reduction: c.o.Reduction, SourceMetadata: c.o.SourceMetadata, Now: c.o.Now, CanCommit: func() bool { return c.valid(g) }})
	if e != nil {
		cancel()
		_ = db.Close()
		return c.startFailure(e)
	}
	g.projector = projector
	c.mu.Lock()
	c.active = g
	c.epoch++
	c.failed = map[string]string{}
	c.failedOrder = nil
	c.sweepFailures = nil
	c.fullFailure = ""
	c.indexFailure = ""
	c.previousFailure = false
	c.servable = len(sources) > 0
	c.watchHealthy = true
	c.storageLimited = false
	c.checkpointBusy = false
	c.cooldownUntil = time.Time{}
	c.status = Status{Mode: config.Mode, State: "building"}
	if len(sources) > 0 {
		c.status.State = "ready"
	}
	if persisted != nil {
		c.status.Discovered = persisted.Discovered
		c.status.Indexed = persisted.Indexed
		c.status.DegradedSources = persisted.Degraded
		c.status.LastErrorCode = persisted.LastErrorCode
		v := time.UnixMilli(persisted.UpdatedAtMs).UTC().Format("2006-01-02T15:04:05.000Z")
		c.status.LastUpdatedAt = &v
	}
	for _, source := range sources {
		if source.State == "degraded" {
			code := "SOURCE_PARSE_DEGRADED"
			if source.LastErrorCode != nil {
				code = *source.LastErrorCode
			}
			c.failed[source.Path] = code
			c.failedOrder = append(c.failedOrder, source.Path)
		}
	}
	if persisted != nil && persisted.State == "degraded" && len(c.failed) == 0 {
		c.fullFailure = "LOCAL_INDEX_DISCOVERY_INCOMPLETE"
		if persisted.LastErrorCode != nil {
			c.fullFailure = *persisted.LastErrorCode
		}
		c.previousFailure = true
	}
	if len(c.failed) > 0 || c.fullFailure != "" {
		c.status.State = "degraded"
		c.status.DegradedSources = c.outstanding()
		code := c.latestFailure("")
		if code == "" {
			code = c.fullFailure
		}
		c.status.LastErrorCode = stringPointer(code)
	}
	c.scheduling = SchedulingMetrics{}
	c.mu.Unlock()
	wo := c.o.WatcherOptions
	wo.Scope = config.Scope
	wo.IsTargetPath = IsPrimaryTranscriptPath
	wo.OnDirty = nil
	wo.OnBatch = func(ctx context.Context, batch ReconciliationBatch) error { return c.reconcile(g, batch) }
	wo.OnFailure = func() {
		c.update(g, func() { c.watchHealthy = false })
		c.markDegraded(g, coded("LOCAL_INDEX_WATCH_FAILED", "Local index watcher failed"), "LOCAL_INDEX_WATCH_FAILED")
	}
	wo.OnRecovered = func() { c.update(g, func() { c.watchHealthy = true }) }
	c.mu.Lock()
	g.watcher, e = NewWatcher(wo)
	c.mu.Unlock()
	if e != nil {
		close(g.initialDone)
		c.stopLocked()
		return c.startFailure(e)
	}
	if e = g.watcher.Start(ownedCtx); e != nil {
		c.update(g, func() { c.watchHealthy = false })
		c.markDegraded(g, e, "LOCAL_INDEX_WATCH_FAILED")
	}
	go func() { defer close(g.initialDone); _ = c.reconcile(g, ReconciliationBatch{FullSweep: true}) }()
	return nil
}
func (c *Coordinator) startFailure(err error) error {
	c.mu.Lock()
	c.status.State = "degraded"
	c.status.LastErrorCode = stringPointer(ClassifyFailure(err))
	c.status.LastUpdatedAt = c.stamp()
	c.mu.Unlock()
	return err
}
func (c *Coordinator) stopLocked() {
	c.mu.Lock()
	g := c.active
	c.active = nil
	c.epoch++
	c.mu.Unlock()
	if g == nil {
		return
	}
	g.cancel()
	if g.watcher != nil {
		_ = g.watcher.Stop(context.Background())
	}
	<-g.initialDone
	c.queueMu.Lock()
	_ = g.db.Close()
	c.queueMu.Unlock()
}
func (c *Coordinator) Stop(context.Context) error {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	c.stopLocked()
	return nil
}
func (c *Coordinator) Rebuild(ctx context.Context) (Status, error) {
	c.rebuildMu.Lock()
	if call := c.pendingRebuild; call != nil {
		c.rebuildMu.Unlock()
		<-call.done
		return call.status, call.err
	}
	call := &rebuildCall{done: make(chan struct{})}
	c.pendingRebuild = call
	c.rebuildMu.Unlock()
	call.status, call.err = c.rebuild(context.WithoutCancel(ctx))
	c.rebuildMu.Lock()
	c.pendingRebuild = nil
	close(call.done)
	c.rebuildMu.Unlock()
	return call.status, call.err
}
func (c *Coordinator) rebuild(ctx context.Context) (Status, error) {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	config := c.configuration()
	c.mu.Lock()
	g := c.active
	c.mu.Unlock()
	if g != nil {
		config.Scope = g.configuration.Scope
		config.DatabasePath = g.configuration.DatabasePath
	}
	c.stopLocked()
	if config.Mode == "off" {
		_ = c.startLocked(ctx, config)
		return c.Status(), nil
	}
	if _, e := c.o.Backup(BackupOptions{Scope: config.Scope, DatabasePath: config.DatabasePath, Reason: "MANUAL_REBUILD", Now: c.o.Now}); e != nil {
		_ = c.startFailure(e)
		return c.Status(), e
	}
	e := c.startLocked(ctx, c.configuration())
	return c.Status(), e
}
func (c *Coordinator) synchronize() bool {
	config := c.configuration()
	c.mu.Lock()
	g := c.active
	if c.reconfiguring {
		c.mu.Unlock()
		return false
	}
	if g == nil {
		if config.Mode == "off" {
			c.status = Status{Mode: "off", State: "off", LastErrorCode: config.WarningCode}
		}
		same := config.Mode == c.status.Mode
		c.mu.Unlock()
		return same
	}
	if sameConfiguration(g.configuration, config) {
		c.mu.Unlock()
		return true
	}
	c.reconfiguring = true
	c.status = Status{Mode: config.Mode, State: "building"}
	if config.Mode == "off" {
		c.status.State = "off"
	}
	c.mu.Unlock()
	go func() {
		c.lifecycleMu.Lock()
		defer c.lifecycleMu.Unlock()
		c.mu.Lock()
		current := c.active
		c.mu.Unlock()
		if current == g {
			c.stopLocked()
			_ = c.startLocked(context.Background(), c.configuration())
		}
		c.mu.Lock()
		c.reconfiguring = false
		c.mu.Unlock()
	}()
	return false
}
func (c *Coordinator) Status() Status {
	c.synchronize()
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.status
	if s.LastErrorCode != nil {
		v := *s.LastErrorCode
		s.LastErrorCode = &v
	}
	if s.LastUpdatedAt != nil {
		v := *s.LastUpdatedAt
		s.LastUpdatedAt = &v
	}
	return s
}
func (c *Coordinator) Mode() string { return c.Status().Mode }
func (c *Coordinator) SchedulingMetrics() SchedulingMetrics {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.scheduling
}
func (c *Coordinator) readGeneration() *primaryGeneration {
	if !c.synchronize() {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active == nil || c.active.ctx.Err() != nil || !c.checkpointBusy && c.o.Now().Before(c.cooldownUntil) {
		return nil
	}
	return c.active
}
func (c *Coordinator) WaitInitial(ctx context.Context) error {
	c.mu.Lock()
	g := c.active
	c.mu.Unlock()
	if g == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-g.initialDone:
		return nil
	}
}
func (c *Coordinator) refreshStorage(g *primaryGeneration) bool {
	checkpoint, e := g.db.CheckpointPassive(g.ctx)
	if e != nil {
		c.update(g, func() { c.storageLimited = true })
		c.markDegraded(g, coded(ClassifyFailure(e), ClassifyFailure(e)), "LOCAL_INDEX_CHECKPOINT_FAILED")
		return false
	}
	if checkpoint.Busy > 0 {
		c.update(g, func() { c.checkpointBusy = true; c.cooldownUntil = c.o.Now().Add(c.o.BusyCooldown) })
		c.mu.Lock()
		allowed := !c.storageLimited
		c.mu.Unlock()
		return allowed
	}
	storage, e := g.db.StorageStats()
	if e != nil {
		c.update(g, func() { c.storageLimited = true })
		c.markDegraded(g, coded(ClassifyFailure(e), ClassifyFailure(e)), "LOCAL_INDEX_CHECKPOINT_FAILED")
		return false
	}
	allowed := true
	c.update(g, func() {
		c.checkpointBusy = false
		c.status.DatabaseBytes = storage.DatabaseBytes
		c.status.WALBytes = storage.WALBytes
		c.storageLimited = storage.DatabaseBytes+storage.WALBytes > c.o.StorageLimitBytes
		if c.storageLimited {
			allowed = false
			c.status.State = "degraded"
			c.status.LastErrorCode = stringPointer("LOCAL_INDEX_SIZE_LIMIT")
			c.status.LastUpdatedAt = c.stamp()
		}
	})
	return allowed
}
func (c *Coordinator) reconcile(g *primaryGeneration, batch ReconciliationBatch) error {
	c.queueMu.Lock()
	c.mu.Lock()
	mutation := c.mutationEpoch
	c.mu.Unlock()
	defer func() {
		c.update(g, func() { c.epoch++; c.coveredMutationEpoch = max(c.coveredMutationEpoch, mutation) })
		c.queueMu.Unlock()
	}()
	if !c.valid(g) {
		return nil
	}
	if batch.FullSweep {
		return c.fullSweep(g)
	}
	return c.targeted(g, batch.Paths)
}
func (c *Coordinator) Reconcile(ctx context.Context, batch ReconciliationBatch) error {
	g := c.readGeneration()
	if g == nil {
		return errors.New("Local index is not running")
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return c.reconcile(g, batch)
}
func (c *Coordinator) outstanding() int64 {
	n := len(c.failed)
	if c.fullFailure != "" {
		n++
	}
	return int64(n)
}
func (c *Coordinator) latestFailure(except string) string {
	last := ""
	for _, path := range c.failedOrder {
		if path != except {
			if code, ok := c.failed[path]; ok {
				last = code
			}
		}
	}
	return last
}
func (c *Coordinator) recordPath(path, code string) {
	if _, exists := c.failed[path]; !exists {
		c.failedOrder = append(c.failedOrder, path)
	}
	c.failed[path] = code
}
func retryCode(result SessionProjectResult, activity bool) string {
	if activity {
		if result.Reason == "changed-during-read" {
			return "LOCAL_INDEX_ACTIVITY_SOURCE_CHANGED"
		}
		return "LOCAL_INDEX_ACTIVITY_TRANSIENT_IO"
	}
	if result.Reason == "changed-during-read" {
		return "LOCAL_INDEX_SOURCE_CHANGED"
	}
	return "LOCAL_INDEX_TRANSIENT_IO"
}
func (c *Coordinator) project(g *primaryGeneration, candidate SessionCandidate, progress *ProjectionProgress, activity bool) (SessionProjectResult, error) {
	if c.o.BeforeProject != nil {
		if e := c.o.BeforeProject(g.ctx, candidate); e != nil {
			return SessionProjectResult{}, e
		}
	}
	if activity {
		return g.projector.ProjectActivity(g.ctx, candidate)
	}
	return g.projector.Project(g.ctx, candidate, progress)
}
func (c *Coordinator) yield(g *primaryGeneration) {
	c.update(g, func() { c.scheduling.YieldCount++ })
	runtime.Gosched()
}
func (c *Coordinator) fullSweep(g *primaryGeneration) error {
	if !c.refreshStorage(g) || !c.valid(g) {
		return nil
	}
	existing, e := g.index.ListSources(g.ctx)
	if e != nil {
		c.markDegraded(g, e, "LOCAL_INDEX_DISCOVERY_FAILED")
		return e
	}
	known, committed, seen := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, source := range existing {
		known[source.Path] = true
		committed[source.Path] = true
	}
	failed := map[string]string{}
	order := []string{}
	generic := 0
	genericCode := ""
	c.update(g, func() {
		c.sweepFailures = failed
		c.indexFailure = ""
		outstanding := c.outstanding()
		if outstanding > 0 {
			c.status.State = "degraded"
		} else if c.status.State != "ready" {
			c.status.State = "building"
		}
		c.status.Discovered = int64(len(known))
		c.status.Indexed = int64(len(committed))
		c.status.DegradedSources = outstanding
		code := c.latestFailure("")
		if code == "" {
			code = c.fullFailure
		}
		c.status.LastErrorCode = stringPointer(code)
	})
	record := func(path, code string) {
		c.update(g, func() {
			if path != "" {
				if _, exists := failed[path]; !exists {
					order = append(order, path)
				}
				failed[path] = code
			} else {
				generic++
				genericCode = code
			}
			union := map[string]bool{}
			for p := range c.failed {
				union[p] = true
			}
			for p := range failed {
				union[p] = true
			}
			n := len(union) + generic
			if c.fullFailure != "" {
				n++
			}
			c.status.State = "degraded"
			c.status.DegradedSources = int64(n)
			c.status.LastErrorCode = stringPointer(code)
		})
	}
	emit := func(candidates []SessionCandidate) error {
		if !c.valid(g) {
			return nil
		}
		unique := []SessionCandidate{}
		for _, candidate := range candidates {
			if !seen[candidate.Path] {
				seen[candidate.Path] = true
				known[candidate.Path] = true
				unique = append(unique, candidate)
			}
		}
		sortCandidates(unique, c.o.Reduction.Compare)
		c.update(g, func() { c.status.Discovered = int64(len(known)) })
		for start := 0; start < len(unique); start += 25 {
			batch := unique[start:min(start+25, len(unique))]
			c.update(g, func() { c.scheduling.MaxBatchSize = max(c.scheduling.MaxBatchSize, len(batch)) })
			for _, candidate := range batch {
				if !c.valid(g) {
					return nil
				}
				target := len(committed)
				if !committed[candidate.Path] {
					target++
				}
				c.mu.Lock()
				progress := &ProjectionProgress{State: "building", Discovered: int64(target), Indexed: int64(target), Degraded: int64(len(failed) + generic), LastErrorCode: c.status.LastErrorCode}
				c.mu.Unlock()
				result, e := c.project(g, candidate, progress, false)
				if !c.valid(g) {
					return nil
				}
				if e != nil {
					record(candidate.Path, errorCode(e, "LOCAL_INDEX_PROJECT_FAILED"))
					continue
				}
				if result.Kind == "retry" {
					record(candidate.Path, retryCode(result, false))
					continue
				}
				committed[candidate.Path] = true
				c.update(g, func() { c.status.Indexed = int64(len(committed)) })
			}
			c.yield(g)
		}
		return nil
	}
	discovery, e := DiscoverTranscriptSources(g.ctx, g.configuration.Scope, c.o.SourceMetadata, c.o.Reduction.Compare, emit)
	if !c.valid(g) {
		return nil
	}
	if e != nil {
		record("", errorCode(e, "LOCAL_INDEX_DISCOVERY_FAILED"))
		discovery.Complete = false
	}
	complete := discovery.Complete && !(discovery.RootMissing && len(existing) > 0)
	if complete {
		stale := []SessionSource{}
		for _, source := range existing {
			if !seen[source.Path] {
				stale = append(stale, source)
			}
		}
		for start := 0; start < len(stale); start += 25 {
			for _, source := range stale[start:min(start+25, len(stale))] {
				if !c.valid(g) {
					return nil
				}
				c.mu.Lock()
				progress := &ProjectionProgress{State: "building", Discovered: int64(len(known)), Indexed: max(c.status.Indexed, int64(len(committed))), Degraded: int64(len(failed) + generic), LastErrorCode: c.status.LastErrorCode}
				c.mu.Unlock()
				result, e := g.projector.Delete(g.ctx, source.Path, progress)
				if e != nil {
					record(source.Path, errorCode(e, "LOCAL_INDEX_DELETE_FAILED"))
				} else if result.Kind == "retry" {
					record(source.Path, retryCode(result, false))
				}
			}
			c.yield(g)
		}
	} else if len(failed)+generic == 0 {
		record("", "LOCAL_INDEX_DISCOVERY_INCOMPLETE")
	}
	c.update(g, func() {
		if complete {
			c.failed = failed
			c.failedOrder = order
			c.fullFailure = genericCode
		} else {
			for _, path := range order {
				c.recordPath(path, failed[path])
			}
			c.fullFailure = genericCode
			if c.fullFailure == "" {
				c.fullFailure = "LOCAL_INDEX_DISCOVERY_INCOMPLETE"
			}
		}
		c.previousFailure = false
		c.sweepFailures = nil
	})
	if !c.valid(g) {
		return nil
	}
	if e = c.activitySweep(g); e != nil && c.valid(g) {
		_ = g.db.Transaction(g.ctx, func(op *Operation) error {
			return WriteActivityBackfill(op, ActivityBackfill{Scope: g.configuration.Scope, State: "degraded", Degraded: 1, LastErrorCode: stringPointer(errorCode(e, "LOCAL_INDEX_ACTIVITY_DISCOVERY_FAILED")), UpdatedAtMs: c.o.Now().UnixMilli()}, nil)
		})
	}
	c.update(g, func() {
		c.servable = true
		c.indexFailure = ""
		n := c.outstanding()
		c.status.State = "ready"
		if n > 0 || !c.watchHealthy {
			c.status.State = "degraded"
		}
		c.status.DegradedSources = n
		code := c.latestFailure("")
		if code == "" {
			code = c.fullFailure
		}
		if code == "" && !c.watchHealthy {
			code = "LOCAL_INDEX_WATCH_FAILED"
		}
		c.status.LastErrorCode = stringPointer(code)
		c.status.LastUpdatedAt = c.stamp()
	})
	c.refreshStorage(g)
	return e
}
func (c *Coordinator) activitySweep(g *primaryGeneration) error {
	existing, e := g.activity.ListSources(g.ctx)
	if e != nil {
		return e
	}
	discovery, e := DiscoverActivitySources(g.ctx, g.configuration.Scope, c.o.SourceMetadata, c.o.Reduction.Compare)
	if e != nil {
		return e
	}
	if !c.valid(g) {
		return nil
	}
	complete := discovery.Complete && !(discovery.RootMissing && len(existing) > 0)
	seen := map[string]bool{}
	indexed, degraded := int64(0), int64(0)
	code := ""
	for start := 0; start < len(discovery.Candidates); start += 25 {
		batch := discovery.Candidates[start:min(start+25, len(discovery.Candidates))]
		for _, candidate := range batch {
			if !c.valid(g) {
				return nil
			}
			seen[candidate.Path] = true
			if candidate.IsSubagent {
				result, e := c.project(g, candidate.SessionCandidate, nil, true)
				if e != nil {
					degraded++
					code = errorCode(e, "LOCAL_INDEX_ACTIVITY_PROJECT_FAILED")
					continue
				}
				if result.Kind == "retry" {
					degraded++
					code = retryCode(result, true)
					continue
				}
			} else {
				source, e := g.activity.GetSource(g.ctx, candidate.Path)
				if e != nil {
					return e
				}
				if source == nil {
					degraded++
					code = "LOCAL_INDEX_ACTIVITY_MAIN_MISSING"
					continue
				}
			}
			indexed++
		}
		if start+len(batch) < len(discovery.Candidates) {
			c.yield(g)
		}
	}
	if complete {
		for _, source := range existing {
			if !c.valid(g) {
				return nil
			}
			if seen[source.Path] {
				continue
			}
			result, e := g.projector.DeleteActivity(g.ctx, source.Path)
			if e != nil {
				return e
			}
			if result.Kind == "retry" {
				degraded++
				code = "LOCAL_INDEX_ACTIVITY_DELETE_FAILED"
			}
		}
	} else {
		degraded++
		if code == "" {
			code = "LOCAL_INDEX_ACTIVITY_DISCOVERY_INCOMPLETE"
		}
	}
	if !c.valid(g) {
		return nil
	}
	state := "degraded"
	if complete && degraded == 0 && indexed == int64(len(discovery.Candidates)) {
		state = "ready"
	}
	var watermark *string
	if len(discovery.Candidates) > 0 {
		watermark = &discovery.Candidates[len(discovery.Candidates)-1].Path
	}
	return g.db.Transaction(g.ctx, func(op *Operation) error {
		return WriteActivityBackfill(op, ActivityBackfill{Scope: g.configuration.Scope, State: state, Discovered: int64(len(discovery.Candidates)), Indexed: indexed, Degraded: degraded, LastErrorCode: stringPointer(code), UpdatedAtMs: c.o.Now().UnixMilli()}, watermark)
	})
}
func (c *Coordinator) targeted(g *primaryGeneration, paths []string) error {
	if !c.refreshStorage(g) || !c.valid(g) {
		return nil
	}
	c.update(g, func() {
		c.indexFailure = ""
		if c.outstanding() > 0 {
			c.status.State = "degraded"
		} else if c.status.State != "building" {
			c.status.State = "ready"
		}
		c.status.DegradedSources = c.outstanding()
		code := c.latestFailure("")
		if code == "" {
			code = c.fullFailure
		}
		c.status.LastErrorCode = stringPointer(code)
	})
	for _, path := range paths {
		if !c.valid(g) {
			return nil
		}
		path, e := filepath.Abs(path)
		if e != nil {
			return e
		}
		existing, e := g.index.GetSource(g.ctx, path)
		if e != nil {
			return e
		}
		count, e := g.index.CountSources(g.ctx)
		if e != nil {
			return e
		}
		c.mu.Lock()
		remaining := c.outstanding()
		if _, ok := c.failed[path]; ok {
			remaining--
		}
		code := c.latestFailure(path)
		if code == "" {
			code = c.fullFailure
		}
		progress := &ProjectionProgress{State: "building", Discovered: c.status.Discovered, Indexed: c.status.Indexed, Degraded: remaining, LastErrorCode: stringPointer(code)}
		if remaining > 0 {
			progress.State = "degraded"
		}
		c.mu.Unlock()
		var candidate *SessionCandidate
		if IsPrimaryTranscriptPath(g.configuration.Scope, path) {
			info, statErr := os.Lstat(path)
			if statErr != nil && !os.IsNotExist(statErr) {
				e = statErr
			} else if statErr == nil && info.Mode().IsRegular() {
				parts := pathParts(g.configuration.Scope, path)
				value, metadataErr := candidateFromFile(path, parts[1][:len(parts[1])-6], parts[0], c.o.SourceMetadata)
				e = metadataErr
				if e == nil {
					candidate = &value
				}
			}
		}
		if e == nil {
			var result SessionProjectResult
			if candidate == nil {
				if existing != nil {
					result, e = g.projector.Delete(g.ctx, path, progress)
				}
			} else {
				target := count
				if existing == nil {
					target++
				}
				progress.Discovered = max(progress.Discovered, target)
				progress.Indexed = max(progress.Indexed, target)
				result, e = c.project(g, *candidate, progress, false)
			}
			if result.Kind == "retry" {
				e = coded(retryCode(result, false), retryCode(result, false))
			}
		}
		if !c.valid(g) {
			return nil
		}
		c.update(g, func() {
			if e != nil {
				c.recordPath(path, errorCode(e, "LOCAL_INDEX_RECONCILE_FAILED"))
			} else {
				delete(c.failed, path)
			}
		})
	}
	count, e := g.index.CountSources(g.ctx)
	if e != nil {
		return e
	}
	c.update(g, func() {
		c.indexFailure = ""
		c.status.State = "ready"
		if c.outstanding() > 0 || !c.watchHealthy {
			c.status.State = "degraded"
		}
		c.status.Discovered = count
		c.status.Indexed = count
		c.status.DegradedSources = c.outstanding()
		code := c.latestFailure("")
		if code == "" {
			code = c.fullFailure
		}
		if code == "" && !c.watchHealthy {
			code = "LOCAL_INDEX_WATCH_FAILED"
		}
		c.status.LastErrorCode = stringPointer(code)
		c.status.LastUpdatedAt = c.stamp()
	})
	c.refreshStorage(g)
	return nil
}
func (c *Coordinator) SessionScopeReady(ctx context.Context) bool {
	g := c.readGeneration()
	if g == nil {
		return false
	}
	c.mu.Lock()
	state := c.status.State
	c.mu.Unlock()
	if state == "off" || state == "degraded" {
		return false
	}
	if state == "ready" {
		return true
	}
	limit := float64(1)
	page, e := g.index.List(ctx, SessionListOptions{Limit: &limit})
	if e != nil {
		c.markDegraded(g, e, "LOCAL_INDEX_READ_FAILED")
		return false
	}
	return page.Total > 0
}
func (c *Coordinator) SourceScopedFailurePaths() []string {
	g := c.readGeneration()
	if g == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.servable || !c.watchHealthy || c.storageLimited || c.indexFailure != "" || c.fullFailure != "" && !c.previousFailure {
		return nil
	}
	paths := map[string]string{}
	order := []string{}
	for _, path := range c.failedOrder {
		if code, ok := c.failed[path]; ok {
			paths[path] = code
			order = append(order, path)
		}
	}
	for path, code := range c.sweepFailures {
		if _, ok := paths[path]; !ok {
			order = append(order, path)
		}
		paths[path] = code
	}
	if len(paths) > 32 {
		return nil
	}
	for _, code := range paths {
		if code != SourceLimit && code != "LOCAL_INDEX_SOURCE_CHANGED" && code != "LOCAL_INDEX_TRANSIENT_IO" {
			return nil
		}
	}
	return append([]string{}, order...)
}
func (c *Coordinator) ActivityScopeReady(ctx context.Context) bool {
	g := c.readGeneration()
	if g == nil {
		return false
	}
	c.mu.Lock()
	allowed := c.status.State != "off" && c.watchHealthy && !c.storageLimited && c.outstanding() == 0
	c.mu.Unlock()
	if !allowed {
		return false
	}
	backfill, e := g.activity.GetBackfill(ctx, g.configuration.Scope)
	if e != nil {
		return false
	}
	return backfill != nil && backfill.State == "ready" && backfill.Degraded == 0 && backfill.Discovered == backfill.Indexed
}
func (c *Coordinator) GetActivityStats(ctx context.Context, rangeName string, now time.Time) (*activitystatsapi.Stats, error) {
	if !c.ActivityScopeReady(ctx) {
		return nil, nil
	}
	g := c.readGeneration()
	if g == nil {
		return nil, nil
	}
	stats, e := g.activity.Aggregate(ctx, rangeName, now)
	if e != nil {
		return nil, nil
	}
	return &stats, nil
}
func (c *Coordinator) List(ctx context.Context, o SessionListOptions) (*SessionPage, error) {
	g := c.readGeneration()
	if g == nil {
		return nil, nil
	}
	page, e := g.index.List(ctx, o)
	if e != nil {
		c.markDegraded(g, e, "LOCAL_INDEX_READ_FAILED")
		return nil, e
	}
	return &page, nil
}
func (c *Coordinator) Get(ctx context.Context, id string) (*protocol.JSONObject, error) {
	g := c.readGeneration()
	if g == nil {
		return nil, nil
	}
	row, e := g.index.Get(ctx, id)
	if e != nil {
		c.markDegraded(g, e, "LOCAL_INDEX_READ_FAILED")
	}
	return row, e
}
func (c *Coordinator) FindFiles(ctx context.Context, id string) ([]SessionFileMatch, error) {
	g := c.readGeneration()
	if g == nil {
		return nil, nil
	}
	rows, e := g.index.FindFiles(ctx, id)
	if e != nil {
		c.markDegraded(g, e, "LOCAL_INDEX_READ_FAILED")
	}
	return rows, e
}
func (c *Coordinator) SearchMetadata(ctx context.Context, q string, limit, offset *float64) (*SessionPage, error) {
	g := c.readGeneration()
	if g == nil {
		return nil, nil
	}
	page, e := g.index.SearchMetadata(ctx, q, limit, offset)
	if e != nil {
		c.markDegraded(g, e, "LOCAL_INDEX_READ_FAILED")
		return nil, e
	}
	return &page, nil
}

func (c *Coordinator) SuggestionMetadata(ctx context.Context, ids []string) ([]protocol.JSONObject, error) {
	g := c.readGeneration()
	if g == nil {
		return nil, nil
	}
	rows, e := g.index.SuggestionMetadata(ctx, ids)
	if e != nil {
		c.markDegraded(g, e, "LOCAL_INDEX_READ_FAILED")
	}
	return rows, e
}
func (c *Coordinator) FindCandidates(ctx context.Context, o ContentQueryOptions) ([]protocol.JSONObject, error) {
	g := c.readGeneration()
	if g == nil {
		return nil, nil
	}
	rows, e := g.index.FindCandidates(ctx, o)
	if e != nil {
		c.markDegraded(g, e, "LOCAL_INDEX_READ_FAILED")
	}
	return rows, e
}
func (c *Coordinator) UpdateTitle(ctx context.Context, id, title string) (bool, error) {
	if !c.synchronize() {
		return false, nil
	}
	c.mu.Lock()
	g := c.active
	c.mu.Unlock()
	if g == nil {
		return false, nil
	}
	updated, e := g.index.UpdateTitle(ctx, id, title)
	if e != nil {
		c.markDegraded(g, e, "LOCAL_INDEX_TITLE_WRITE_FAILED")
	}
	if updated {
		c.mu.Lock()
		c.epoch++
		c.mu.Unlock()
	}
	return updated, e
}
func (c *Coordinator) Locators(ctx context.Context, path string, types *[]string) (*LocatorPage, error) {
	g := c.readGeneration()
	if g == nil {
		return nil, nil
	}
	page, e := g.index.Locators(ctx, path, types)
	if e != nil {
		c.markDegraded(g, e, "LOCAL_INDEX_READ_FAILED")
	}
	return page, e
}
func (c *Coordinator) ReadEntries(ctx context.Context, path, project string, types *[]string) (*TargetedEntryRead, *LocatorPage, error) {
	g := c.readGeneration()
	if g == nil {
		return nil, nil, nil
	}
	page, e := g.index.Locators(ctx, path, types)
	if e != nil {
		return nil, nil, e
	}
	if page == nil {
		return nil, nil, nil
	}
	var result *TargetedEntryRead
	e = c.o.Budget.With(ctx, sessionstore.MetadataLane, func() error {
		result = ReadEntriesByLocator(ctx, path, filepath.Join(g.configuration.Scope, "projects"), project, *page)
		return nil
	})
	if !c.valid(g) {
		return nil, nil, nil
	}
	return result, page, e
}

func (c *Coordinator) Epoch() uint64 { c.mu.Lock(); defer c.mu.Unlock(); return c.epoch }
func (c *Coordinator) ConsumerAvailable() bool {
	if c.readGeneration() == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status.Mode == "on" && c.status.State != "off" && c.status.State != "degraded" && c.coveredMutationEpoch == c.mutationEpoch && !c.o.Now().Before(c.consumerCooldownUntil)
}
func (c *Coordinator) ConsumerReadAllowed() bool {
	if c.readGeneration() == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.coveredMutationEpoch == c.mutationEpoch && !c.o.Now().Before(c.consumerCooldownUntil)
}
func (c *Coordinator) NoteConsumerFailure() {
	c.mu.Lock()
	c.consumerCooldownUntil = c.o.Now().Add(c.o.BusyCooldown)
	c.mu.Unlock()
}
func (c *Coordinator) NoteMutation(path string) {
	c.mu.Lock()
	c.epoch++
	c.mutationEpoch++
	g := c.active
	var watcher *Watcher
	if g != nil {
		watcher = g.watcher
	}
	c.mu.Unlock()
	if watcher != nil {
		if path == "" {
			watcher.QueueFullSweep()
		} else {
			watcher.QueuePath(path)
		}
	}
}
