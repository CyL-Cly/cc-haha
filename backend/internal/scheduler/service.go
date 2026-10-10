package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type runningTask struct {
	cancel  context.CancelFunc
	started time.Time
	runID   string
}
type Service struct {
	options        Options
	store          *taskStore
	mu             sync.Mutex
	running        map[string]runningTask
	minutes        map[string]string
	startCancel    context.CancelFunc
	lifetimeCancel context.CancelFunc
	lifetime       context.Context
	workers        sync.WaitGroup
	projection     *readModel
	stopped        bool
	lifecycle      sync.Mutex
	shutdownDone   chan struct{}
}

func New(o Options) (*Service, error) {
	if !filepath.IsAbs(o.Home) || !filepath.IsAbs(o.ConfigDir) || o.Environment == nil || o.Now == nil || o.RandomBytes == nil || o.Execute == nil || o.CreateSession == nil || o.DeletePlaceholder == nil || o.AppendSessionMetadata == nil || o.LogError == nil || o.GetProcessEnvironment == nil || o.ProviderRuntimeEnv == nil || o.NetworkEnvironment == nil || o.AgentTeamsEnabled == nil || o.OfficialOAuthEnv == nil {
		return nil, errors.New("scheduler requires actual owned paths/environment/clock/random/session/provider/network/OAuth/error producers")
	}
	if o.Files == nil {
		o.Files = OSFiles{}
	}
	if o.Notify == nil && o.GetRawAdapterConfig != nil {
		do := o.NotificationHTTPDo
		if do == nil {
			do = http.DefaultClient.Do
		}
		log := o.NotificationLog
		if log == nil {
			log = func(_ string, message string, details any) {
				if details == nil {
					o.LogError(errors.New(message))
				} else {
					o.LogError(fmt.Errorf("%s %v", message, details))
				}
			}
		}
		notifier, e := NewNotifier(NotificationOptions{GetRawConfig: o.GetRawAdapterConfig, Do: do, Log: log})
		if e != nil {
			return nil, e
		}
		o.Notify = notifier.Send
	}
	projection := &readModel{options: o}
	lifetime, lifetimeCancel := context.WithCancel(context.Background())
	o.afterRunWrite = projection.afterWrite
	return &Service{options: o, store: &taskStore{o}, running: map[string]runningTask{}, minutes: map[string]string{}, lifetime: lifetime, lifetimeCancel: lifetimeCancel, projection: projection}, nil
}
func (s *Service) ListTasks(ctx context.Context) ([]protocol.JSONObject, error) {
	if e := ctx.Err(); e != nil {
		return nil, context.Cause(ctx)
	}
	return s.store.list()
}
func (s *Service) CreateTask(ctx context.Context, input protocol.JSONObject) (protocol.JSONObject, error) {
	if e := ctx.Err(); e != nil {
		return protocol.JSONObject{}, context.Cause(ctx)
	}
	return s.store.create(input)
}
func (s *Service) UpdateTask(ctx context.Context, id string, input protocol.JSONObject) (protocol.JSONObject, error) {
	if e := ctx.Err(); e != nil {
		return protocol.JSONObject{}, context.Cause(ctx)
	}
	return s.store.update(id, input)
}
func (s *Service) DeleteTask(ctx context.Context, id string) error {
	if e := ctx.Err(); e != nil {
		return context.Cause(ctx)
	}
	return s.store.delete(id)
}
func minuteKey(date time.Time) string { return date.Format("2006-1-2-15-4") }
func iso(date time.Time) string       { return date.UTC().Format("2006-01-02T15:04:05.000Z") }
func stringID(id int) string          { return strconv.Itoa(id) }
func (s *Service) Start(ctx context.Context) error {
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	if s.shutdownDone != nil {
		select {
		case <-s.shutdownDone:
			s.shutdownDone = nil
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	if e := ctx.Err(); e != nil {
		return context.Cause(ctx)
	}
	s.mu.Lock()
	if s.startCancel != nil {
		s.mu.Unlock()
		return nil
	}
	lifetime, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s.startCancel = cancel
	if s.stopped {
		s.lifetimeCancel = cancel
		s.lifetime = lifetime
	} else {
		cancel()
	}
	s.stopped = false
	s.projection.mu.Lock()
	s.projection.closing = false
	s.projection.stopping.Store(false)
	s.projection.mu.Unlock()
	lifetime = s.lifetime
	s.startCancel = s.lifetimeCancel
	s.workers.Add(2)
	s.mu.Unlock()
	go func() {
		defer s.workers.Done()
		if e := s.cleanupStaleRuns(); e != nil {
			s.options.LogError(e)
		}
	}()
	go func() {
		defer s.workers.Done()
		_ = s.Tick(lifetime)
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-lifetime.Done():
				return
			case <-ticker.C:
				_ = s.Tick(lifetime)
			}
		}
	}()
	return nil
}
func (s *Service) Stop(ctx context.Context) error {
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	s.mu.Lock()
	s.stopped = true
	s.lifetimeCancel()
	if s.startCancel != nil {
		s.startCancel()
		s.startCancel = nil
	}
	for id, run := range s.running {
		run.cancel()
		delete(s.running, id)
	}
	s.mu.Unlock()
	done := s.shutdownDone
	if done == nil {
		done = make(chan struct{})
		s.shutdownDone = done
		go func() { s.workers.Wait(); _ = s.projection.stop(context.Background()); close(done) }()
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
func (s *Service) Tick(ctx context.Context) error {
	tasks, e := s.ListTasks(ctx)
	if e != nil {
		s.options.LogError(e)
		return e
	}
	now := s.options.Now()
	key := minuteKey(now)
	for _, task := range tasks {
		if string(task.Get("enabled")) == "false" {
			continue
		}
		id := task.String("id")
		s.mu.Lock()
		_, active := s.running[id]
		already := s.minutes[id] == key
		s.mu.Unlock()
		if active || already {
			continue
		}
		if fired := task.Get("lastFiredAt"); jsTruthy(fired) {
			millis, valid, e := dateMs(fired)
			if e != nil {
				s.options.LogError(e)
				return e
			}
			if valid && minuteKey(time.UnixMilli(int64(millis)).In(now.Location())) == key {
				continue
			}
		}
		var cron string
		if e = json.Unmarshal(task.Get("cron"), &cron); e != nil || string(task.Get("cron")) == "null" {
			e = &CapabilityError{"original malformed cron.trim exception"}
			s.options.LogError(e)
			return e
		}
		if CronMatches(cron, now) {
			s.mu.Lock()
			s.minutes[id] = key
			s.mu.Unlock()
			if e = s.dispatch(task, false); e != nil {
				s.options.LogError(e)
			}
		}
	}
	return nil
}
func (s *Service) RunNow(ctx context.Context, id string) error {
	tasks, e := s.ListTasks(ctx)
	if e != nil {
		return e
	}
	for _, task := range tasks {
		if task.String("id") == id {
			return s.dispatch(task, true)
		}
	}
	return missing("Task " + id + " not found")
}
func (s *Service) dispatch(task protocol.JSONObject, manual bool) error {
	s.mu.Lock()
	ctx := s.lifetime
	if ctx.Err() != nil || s.stopped {
		s.mu.Unlock()
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		return errors.New("scheduled task dispatcher is stopped")
	}
	s.workers.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.workers.Done()
		if _, e := s.ExecuteTask(ctx, task, manual); e != nil {
			s.options.LogError(e)
		}
	}()
	return nil
}
func (s *Service) workDir(task protocol.JSONObject) string {
	dir := task.String("folderPath")
	if dir == "" {
		dir = s.options.Home
	} else if info, e := s.options.Files.Stat(dir); e != nil || !info.IsDir() {
		dir = s.options.Home
	}
	if canonical, e := s.options.Files.Realpath(dir); e == nil {
		return canonical
	}
	return dir
}
func (s *Service) ExecuteTask(ctx context.Context, task protocol.JSONObject, manual bool) (protocol.JSONObject, error) {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return protocol.JSONObject{}, errors.New("scheduled task dispatcher is stopped")
	}
	s.workers.Add(1)
	lifetime := s.lifetime
	s.mu.Unlock()
	defer s.workers.Done()
	owned, ownedCancel := context.WithCancel(ctx)
	stopCancel := context.AfterFunc(lifetime, ownedCancel)
	defer func() { stopCancel(); ownedCancel() }()
	ctx = owned
	id := task.String("id")
	s.mu.Lock()
	existing, active := s.running[id]
	s.mu.Unlock()
	name := task.Get("name")
	if !jsTruthy(name) {
		if _, e := protocol.DecodeJavaScriptJSONString(task.Get("prompt")); e != nil {
			return protocol.JSONObject{}, &CapabilityError{"original malformed prompt.slice exception"}
		}
		name = truncateJSONText(task.Get("prompt"), 60)
	}
	if active {
		return protocol.NewJSONObject(protocol.JSONField{Name: "id", Value: quoted(existing.runID)}, protocol.JSONField{Name: "taskId", Value: quoted(id)}, protocol.JSONField{Name: "taskName", Value: name}, protocol.JSONField{Name: "startedAt", Value: quoted(iso(existing.started))}, protocol.JSONField{Name: "status", Value: quoted("running")}, protocol.JSONField{Name: "prompt", Value: task.Get("prompt")}), nil
	}
	runID, e := s.store.random(6)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	started := s.options.Now()
	workDir := s.workDir(task)
	sessionID := ""
	if manual {
		if id, e := s.options.CreateSession(ctx, workDir, "bypassPermissions"); e == nil {
			sessionID = id
			_ = s.options.DeletePlaceholder(ctx, sessionID)
		}
	}
	run := protocol.NewJSONObject(protocol.JSONField{Name: "id", Value: quoted(runID)}, protocol.JSONField{Name: "taskId", Value: quoted(id)}, protocol.JSONField{Name: "taskName", Value: name}, protocol.JSONField{Name: "startedAt", Value: quoted(iso(started))}, protocol.JSONField{Name: "status", Value: quoted("running")}, protocol.JSONField{Name: "prompt", Value: task.Get("prompt")})
	if sessionID != "" {
		run.Set("sessionId", quoted(sessionID))
	}
	if e = s.store.lastFired(id, iso(started)); e != nil {
		return protocol.JSONObject{}, e
	}
	if e = s.store.mutateRuns(run, false); e != nil {
		return protocol.JSONObject{}, e
	}
	env, e := BuildTaskEnvironment(ctx, s.options, task, workDir)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	input := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("user")}, protocol.JSONField{Name: "message", Value: raw(protocol.NewJSONObject(protocol.JSONField{Name: "role", Value: quoted("user")}, protocol.JSONField{Name: "content", Value: raw([]protocol.JSONObject{protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("text")}, protocol.JSONField{Name: "text", Value: task.Get("prompt")})})}))}, protocol.JSONField{Name: "parent_tool_use_id", Value: []byte(`null`)}, protocol.JSONField{Name: "session_id", Value: quoted(sessionID)})
	timeoutMs := ResolveTaskTimeoutMs(s.options.Environment())
	modelContext, cancel := context.WithTimeout(ctx, timerDuration(timeoutMs))
	defer cancel()
	s.mu.Lock()
	s.running[id] = runningTask{cancel: cancel, started: s.options.Now(), runID: runID}
	s.mu.Unlock()
	var outputMu sync.Mutex
	var output strings.Builder
	result, failure := s.options.Execute(modelContext, ExecutionRequest{Task: task.Clone(), WorkDir: workDir, SessionID: sessionID, Input: input, Environment: env}, func(value json.RawMessage) {
		outputMu.Lock()
		defer outputMu.Unlock()
		output.Write(value)
		output.WriteByte('\n')
	})
	cancel()
	s.mu.Lock()
	delete(s.running, id)
	s.mu.Unlock()
	completed := s.options.Now()
	run.Set("completedAt", quoted(iso(completed)))
	if failure != nil {
		run.Set("status", quoted("failed"))
		run.Set("error", quoted(failure.Error()))
		run.Set("durationMs", raw(completed.UnixMilli()-started.UnixMilli()))
	} else {
		status := "failed"
		if float64(completed.UnixMilli()-started.UnixMilli()) >= timeoutMs {
			status = "timeout"
		} else if result.ExitCode == 0 {
			status = "completed"
		}
		run.Set("status", quoted(status))
		outputMu.Lock()
		text := extractAssistantText(output.String())
		outputMu.Unlock()
		run.Set("output", truncateJSONText(text, 50000))
		run.Set("exitCode", raw(result.ExitCode))
		run.Set("durationMs", raw(completed.UnixMilli()-started.UnixMilli()))
		if result.ExitCode != 0 {
			run.Set("error", truncateJSONText(quoted(result.Stderr), 5000))
		}
	}
	if sessionID != "" {
		_ = s.options.AppendSessionMetadata(context.WithoutCancel(ctx), sessionID, protocol.NewJSONObject(protocol.JSONField{Name: "workDir", Value: quoted(workDir)}, protocol.JSONField{Name: "permissionMode", Value: quoted("bypassPermissions")}))
	}
	if e = s.store.mutateRuns(run, true); e != nil {
		return protocol.JSONObject{}, e
	}
	if failure == nil {
		var notification protocol.JSONObject
		if notification.UnmarshalJSON(task.Get("notification")) == nil && jsTruthy(notification.Get("enabled")) {
			var channels []string
			_ = json.Unmarshal(notification.Get("channels"), &channels)
			external := false
			for _, channel := range channels {
				external = external || channel == "telegram" || channel == "feishu"
			}
			if len(channels) > 0 && external {
				if s.options.Notify == nil {
					s.options.LogError(&CapabilityError{"actual configured Telegram/Feishu notification producers"})
				} else {
					s.mu.Lock()
					if !s.stopped {
						s.workers.Add(1)
					}
					stopped := s.stopped
					s.mu.Unlock()
					if !stopped {
						go func() {
							defer s.workers.Done()
							if e := s.options.Notify(context.WithoutCancel(ctx), run.Clone(), notification.Clone()); e != nil {
								s.options.LogError(e)
							}
						}()
					}
				}
			}
		}
		if !jsTruthy(task.Get("recurring")) {
			_, _ = s.store.update(id, protocol.NewJSONObject(protocol.JSONField{Name: "enabled", Value: []byte(`false`)}))
		}
	}
	return run, nil
}
func (s *Service) cleanupStaleRuns() error {
	path := s.store.runsPath()
	lock := pathLock(path)
	lock.Lock()
	defer lock.Unlock()
	file, rows, _, e := s.store.read(path, "runs")
	if e != nil {
		return e
	}
	changed := false
	now := s.options.Now()
	timeout := ResolveTaskTimeoutMs(s.options.Environment())
	for i, run := range rows {
		if run.String("status") != "running" {
			continue
		}
		started, valid, e := dateMs(run.Get("startedAt"))
		if e != nil {
			return e
		}
		if !valid {
			continue
		}
		duration := now.UnixMilli() - int64(started)
		if float64(duration) > timeout+60000 {
			run.Set("status", quoted("failed"))
			run.Set("error", quoted("Process terminated before task could complete"))
			run.Set("completedAt", quoted(iso(now)))
			run.Set("durationMs", raw(duration))
			rows[i] = run
			changed = true
		}
	}
	if !changed {
		return nil
	}
	file.Set("runs", raw(rows))
	data, e := pretty(file)
	if e != nil {
		return e
	}
	random, e := s.store.random(6)
	if e != nil {
		return e
	}
	temp := path + ".tmp." + stringID(processID()) + "." + random
	if e = s.options.Files.MkdirAll(filepath.Dir(path), 0777); e != nil {
		return e
	}
	if e = s.options.Files.WriteFile(temp, data, 0666); e == nil {
		e = s.options.Files.Rename(temp, path)
	}
	if e != nil {
		_ = s.options.Files.Remove(temp)
	} else if s.options.afterRunWrite != nil {
		s.options.afterRunWrite(path, data, rows)
	}
	return e
}
