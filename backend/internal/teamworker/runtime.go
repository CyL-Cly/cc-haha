package teamworker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamplan"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
)

// WorkerRequest is a frozen approved main-session launch. Prepare must use the
// shared native desktop session and acknowledge its model before returning.
type WorkerRequest struct {
	ID, WorkDir, Model, Effort, PermissionMode string
	ProviderID                                 *string
	Worker                                     Start
}
type RuntimeOptions struct {
	Config             *teamstore.Config
	Plans              *teamplan.Store
	Tasks              *taskstore.Service
	Mailbox            *teamstore.Mailbox
	Validation         ValidationOptions
	Now                func() time.Time
	UUID               func() (string, error)
	Connected          func(string) bool
	WaitWorkersStopped func(context.Context, string) error
	Prepare            func(context.Context, WorkerRequest) (string, error)
	Release            func(context.Context, string, string, func() bool) (bool, error)
	StopWorker         func(context.Context, string) error
	// Snapshot updates the real leader's team context and SDK acknowledgment.
	Snapshot   func(context.Context, string, string, json.RawMessage) error
	Observe    func(string, func(protocol.JSONObject)) (func(), error)
	SendLeader func(context.Context, string, string) (bool, error)
	Log        func(error)
}
type Runtime struct {
	options  RuntimeOptions
	mu       sync.Mutex
	launches map[string]*launch
	closed   bool
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}
type launch struct {
	plan            protocol.JSONObject
	stopped         atomic.Bool
	released        atomic.Bool
	mu              sync.Mutex
	children        []string
	removeObservers []func()
	cancel          context.CancelFunc
}
type ExecutionInterruptedError struct{ error }

func (ExecutionInterruptedError) ExecutionStarted() bool { return true }
func object(data json.RawMessage) protocol.JSONObject {
	var v protocol.JSONObject
	_ = v.UnmarshalJSON(data)
	return v
}
func rows(data json.RawMessage) []protocol.JSONObject {
	out := []protocol.JSONObject{}
	_ = json.Unmarshal(data, &out)
	return out
}
func stringsIn(data json.RawMessage) []string {
	out := []string{}
	_ = json.Unmarshal(data, &out)
	return out
}
func number(data json.RawMessage) float64 { var n float64; _ = json.Unmarshal(data, &n); return n }
func NewRuntime(o RuntimeOptions) (*Runtime, error) {
	if o.Config == nil || o.Plans == nil || o.Tasks == nil || o.Mailbox == nil || o.Now == nil || o.UUID == nil || o.Connected == nil || o.WaitWorkersStopped == nil || o.Prepare == nil || o.Release == nil || o.StopWorker == nil || o.Snapshot == nil || o.Observe == nil || o.SendLeader == nil || o.Log == nil {
		return nil, errors.New("Team runtime requires actual shared session, model barrier, task/config/mailbox and lifecycle producers")
	}
	o.Validation.Config = o.Config
	o.Validation.Plans = o.Plans
	if o.Validation.PermissionMode == nil || o.Validation.Routing == nil {
		return nil, errors.New("Team runtime requires original leader permissions and saved-provider routing")
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Runtime{options: o, launches: map[string]*launch{}, ctx: ctx, cancel: cancel}, nil
}
func (r *Runtime) ServiceBindings() teamplan.Runtime {
	return teamplan.Runtime{Validate: r.Validate, Launch: r.Launch, Stop: r.Stop, IsRunning: func(_ context.Context, id string) (bool, error) { return r.IsRunning(id), nil }, NotifyLeader: r.NotifyLeader}
}
func (r *Runtime) Validate(ctx context.Context, plan protocol.JSONObject) (protocol.JSONObject, error) {
	return ValidateRuntime(ctx, plan, r.options.Validation)
}
func (r *Runtime) IsRunning(id string) bool {
	r.mu.Lock()
	l := r.launches[id]
	r.mu.Unlock()
	return l != nil && !l.stopped.Load() && r.options.Connected(l.plan.String("sessionId"))
}

// StartBarrier preserves the original sequential prepare/all-ready/release
// order. A release failure stops every prepared child and is never replayed.
func StartBarrier[T any](ctx context.Context, members []T, prepare func(context.Context, T) (string, error), release func(context.Context, T, string) error, stop func(context.Context, string) error) ([]string, error) {
	ids := []string{}
	rollback := func() {
		var wg sync.WaitGroup
		for _, id := range ids {
			wg.Add(1)
			go func(id string) { defer wg.Done(); _ = stop(context.WithoutCancel(ctx), id) }(id)
		}
		wg.Wait()
	}
	for _, member := range members {
		id, err := prepare(ctx, member)
		if err != nil {
			rollback()
			return nil, err
		}
		ids = append(ids, id)
	}
	for i, member := range members {
		if err := release(ctx, member, ids[i]); err != nil {
			rollback()
			return nil, err
		}
	}
	return ids, nil
}

func (r *Runtime) MaterializeTasks(plan protocol.JSONObject) (map[string]string, error) {
	list := teamstore.SanitizeName(plan.String("teamName"))
	existing, err := r.options.Tasks.List(list)
	if err != nil {
		return nil, err
	}
	approved := object(plan.Get("approvedSnapshot"))
	tasks, members := rows(approved.Get("tasks")), rows(approved.Get("members"))
	mapping := map[string]string{}
	for _, task := range tasks {
		var old *protocol.JSONObject
		for i, entry := range existing {
			metadata := object(entry.Get("metadata"))
			if metadata.String("teamPlanId") == plan.String("planId") && metadata.String("teamPlanTaskId") == task.String("id") {
				old = &existing[i]
				break
			}
		}
		if old == nil {
			for i, entry := range existing {
				if entry.String("id") == task.String("id") && entry.String("subject") == task.String("subject") && object(entry.Get("metadata")).String("teamPlanId") == "" {
					old = &existing[i]
					break
				}
			}
		}
		if old != nil {
			mapping[task.String("id")] = old.String("id")
			continue
		}
		created, err := r.options.Tasks.Create(list, protocol.NewJSONObject(protocol.JSONField{Name: "subject", Value: task.Get("subject")}, protocol.JSONField{Name: "description", Value: raw(task.String("description"))}, protocol.JSONField{Name: "status", Value: raw("pending")}, protocol.JSONField{Name: "blocks", Value: raw([]string{})}, protocol.JSONField{Name: "blockedBy", Value: raw([]string{})}, protocol.JSONField{Name: "metadata", Value: raw(protocol.NewJSONObject(protocol.JSONField{Name: "teamPlanId", Value: plan.Get("planId")}, protocol.JSONField{Name: "teamPlanTaskId", Value: task.Get("id")}))}))
		if err != nil {
			return nil, err
		}
		mapping[task.String("id")] = created.TaskID
	}
	for _, task := range tasks {
		var owner json.RawMessage
		for _, member := range members {
			if member.String("id") == task.String("ownerId") {
				owner = member.Get("name")
				break
			}
		}
		blockedBy, blocks := []string{}, []string{}
		for _, id := range stringsIn(task.Get("dependencies")) {
			blockedBy = append(blockedBy, mapping[id])
		}
		for _, other := range tasks {
			for _, dep := range stringsIn(other.Get("dependencies")) {
				if dep == task.String("id") {
					blocks = append(blocks, mapping[other.String("id")])
					break
				}
			}
		}
		metadata := protocol.NewJSONObject()
		for _, entry := range existing {
			if entry.String("id") == mapping[task.String("id")] {
				previous := object(entry.Get("metadata"))
				if previous.IsObject() {
					metadata = previous.Clone()
				}
				break
			}
		}
		metadata.Set("teamPlanId", plan.Get("planId"))
		metadata.Set("teamPlanTaskId", task.Get("id"))
		_, err := r.options.Tasks.UpdateTask(list, raw(mapping[task.String("id")]), protocol.NewJSONObject(protocol.JSONField{Name: "subject", Value: task.Get("subject")}, protocol.JSONField{Name: "description", Value: raw(task.String("description"))}, protocol.JSONField{Name: "owner", Value: owner}, protocol.JSONField{Name: "blockedBy", Value: raw(blockedBy)}, protocol.JSONField{Name: "blocks", Value: raw(blocks)}, protocol.JSONField{Name: "metadata", Value: raw(metadata)}))
		if err != nil {
			return nil, err
		}
	}
	return mapping, nil
}
func (r *Runtime) generation(plan protocol.JSONObject) (*protocol.JSONObject, error) {
	team := r.options.Config.Read(plan.String("teamName"))
	inc, err := r.options.Plans.Incarnation(plan.String("teamName"), plan.String("sessionId"))
	if err != nil || team == nil || team.String("leadSessionId") != plan.String("sessionId") || inc != plan.String("incarnationId") {
		return nil, errors.New("Team generation no longer exists")
	}
	return team, nil
}
func (r *Runtime) authorized(plan protocol.JSONObject) bool {
	latest, err := r.options.Plans.Read(plan.String("teamName"))
	return err == nil && latest != nil && latest.String("planId") == plan.String("planId") && latest.String("incarnationId") == plan.String("incarnationId") && latest.String("state") == "launching" && number(latest.Get("revision")) == number(plan.Get("revision")) && object(latest.Get("approvedSnapshot")).String("requestId") == object(plan.Get("approvedSnapshot")).String("requestId")
}
func (r *Runtime) cancelled(l *launch) bool {
	return l.stopped.Load() || !r.options.Connected(l.plan.String("sessionId"))
}
func (r *Runtime) Launch(ctx context.Context, plan protocol.JSONObject) (result protocol.JSONObject, failure error) {
	approved := object(plan.Get("approvedSnapshot"))
	if !approved.IsObject() || number(approved.Get("revision")) != number(plan.Get("revision"))-1 || plan.String("state") != "launching" {
		return result, errors.New("Team plan is not approved for launch")
	}
	if err := r.options.WaitWorkersStopped(ctx, plan.String("sessionId")); err != nil {
		return result, err
	}
	if !r.authorized(plan) {
		return result, errors.New("Team launch authorization has been revoked")
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return result, context.Canceled
	}
	if r.launches[plan.String("planId")] != nil {
		r.mu.Unlock()
		return result, errors.New("Team plan already has a launch in progress")
	}
	if !r.options.Connected(plan.String("sessionId")) {
		r.mu.Unlock()
		return result, errors.New("Team leader must be connected before launching")
	}
	l := &launch{plan: plan.Clone(), children: []string{}, removeObservers: []func(){}}
	r.launches[plan.String("planId")] = l
	r.mu.Unlock()
	executionStarted := false
	defer func() {
		if failure != nil {
			_ = r.Stop(context.WithoutCancel(ctx), plan.String("planId"))
			if executionStarted {
				failure = ExecutionInterruptedError{failure}
			}
		}
	}()
	permissionMode, err := r.options.Validation.PermissionMode(ctx, plan.String("sessionId"))
	if err != nil {
		return result, err
	}
	var createdAt json.RawMessage
	list := teamstore.SanitizeName(plan.String("teamName"))
	if err = r.options.Tasks.WithTeamLifecycleLock(list, func() error {
		team, err := r.generation(plan)
		if err != nil {
			return err
		}
		team.Set("reviewRequired", raw(true))
		createdAt = append(json.RawMessage(nil), team.Get("createdAt")...)
		return r.options.Config.Write(plan.String("teamName"), *team)
	}); err != nil {
		return result, err
	}
	mapping, err := r.MaterializeTasks(plan)
	if err != nil {
		return result, err
	}
	members := rows(approved.Get("members"))
	memberIDs := protocol.NewJSONObject()
	_, err = StartBarrier(ctx, members, func(ctx context.Context, member protocol.JSONObject) (string, error) {
		if r.cancelled(l) {
			return "", errors.New("Team launch cancelled")
		}
		id, err := r.options.UUID()
		if err != nil {
			return "", err
		}
		l.mu.Lock()
		l.children = append(l.children, id)
		l.mu.Unlock()
		memberIDs.Set(member.String("id"), raw(id))
		snapshot := object(member.Get("agentSnapshot"))
		if !snapshot.IsObject() {
			return "", fmt.Errorf("Missing approved preset for %s", member.String("name"))
		}
		var tools *[]string
		if snapshot.Get("tools") != nil {
			names := AddEssentialTools(stringsIn(snapshot.Get("tools")))
			tools = &names
		}
		runtime := object(member.Get("runtime"))
		var provider *string
		if runtime.String("providerId") != "claude-official" {
			v := runtime.String("providerId")
			provider = &v
		}
		definition := snapshot.Clone()
		definition.Delete("initialPrompt")
		definition.Set("effort", runtime.Get("effortLevel"))
		actual, err := r.options.Prepare(ctx, WorkerRequest{ID: id, WorkDir: plan.String("workDir"), Model: runtime.String("modelId"), Effort: runtime.String("effortLevel"), PermissionMode: permissionMode, ProviderID: provider, Worker: Start{ParentSessionID: plan.String("sessionId"), TeamName: plan.String("teamName"), MemberID: member.String("id"), Name: member.String("name"), SystemPrompt: snapshot.String("systemPrompt"), Tools: tools, AgentDefinition: definition}})
		if err != nil {
			_ = r.options.StopWorker(context.WithoutCancel(ctx), id)
			return "", err
		}
		if actual != id {
			_ = r.options.StopWorker(context.WithoutCancel(ctx), actual)
			return "", errors.New("Team prepared session identity changed")
		}
		return id, nil
	}, func(ctx context.Context, member protocol.JSONObject, id string) error {
		if r.cancelled(l) {
			return errors.New("Team launch cancelled")
		}
		if member.String("id") == members[0].String("id") {
			if err := r.options.Tasks.WithTeamLifecycleLock(list, func() error {
				team := r.options.Config.Read(plan.String("teamName"))
				if team == nil || number(team.Get("createdAt")) != number(createdAt) {
					return errors.New("Team generation changed during launch")
				}
				entries := rows(team.Get("members"))
				for _, entry := range members {
					for _, old := range entries {
						if old.String("name") == entry.String("name") {
							return fmt.Errorf("Member already exists: %s", entry.String("name"))
						}
					}
					runtime := object(entry.Get("runtime"))
					next := protocol.NewJSONObject(protocol.JSONField{Name: "agentId", Value: raw(entry.String("name") + "@" + plan.String("teamName"))}, protocol.JSONField{Name: "name", Value: entry.Get("name")}, protocol.JSONField{Name: "agentType", Value: entry.Get("agentType")}, protocol.JSONField{Name: "model", Value: runtime.Get("modelId")}, protocol.JSONField{Name: "providerId", Value: runtime.Get("providerId")}, protocol.JSONField{Name: "providerName", Value: entry.Get("providerName")}, protocol.JSONField{Name: "effortLevel", Value: runtime.Get("effortLevel")}, protocol.JSONField{Name: "planMemberId", Value: entry.Get("id")}, protocol.JSONField{Name: "joinedAt", Value: raw(r.options.Now().UnixMilli())}, protocol.JSONField{Name: "tmuxPaneId", Value: raw("")}, protocol.JSONField{Name: "cwd", Value: plan.Get("workDir")}, protocol.JSONField{Name: "subscriptions", Value: raw([]string{})}, protocol.JSONField{Name: "sessionId", Value: memberIDs.Get(entry.String("id"))}, protocol.JSONField{Name: "backendType", Value: raw("process")}, protocol.JSONField{Name: "isActive", Value: raw(true)})
					entries = append(entries, next)
				}
				team.Set("members", raw(entries))
				return r.options.Config.Write(plan.String("teamName"), *team)
			}); err != nil {
				return err
			}
			if err := r.options.Snapshot(ctx, plan.String("sessionId"), plan.String("teamName"), createdAt); err != nil {
				return err
			}
		}
		remove, err := r.options.Observe(id, func(message protocol.JSONObject) {
			if message.String("type") == "result" {
				r.observeResult(l, member, id, createdAt, message)
			}
		})
		if err != nil {
			return err
		}
		l.mu.Lock()
		l.removeObservers = append(l.removeObservers, remove)
		l.mu.Unlock()
		assigned := []protocol.JSONObject{}
		for _, task := range rows(approved.Get("tasks")) {
			if task.String("ownerId") == member.String("id") {
				next := task.Clone()
				next.Set("id", raw(mapping[task.String("id")]))
				deps := []string{}
				for _, dep := range stringsIn(task.Get("dependencies")) {
					deps = append(deps, mapping[dep])
				}
				next.Set("dependencies", raw(deps))
				assigned = append(assigned, next)
			}
		}
		prefix := ""
		if value := object(member.Get("agentSnapshot")).String("initialPrompt"); value != "" {
			prefix = value + "\n"
		}
		text := prefix + member.String("prompt") + "\n\nApproved shared tasks (use TaskGet/TaskUpdate; respect dependencies):\n" + string(raw(assigned))
		return r.options.Tasks.WithTeamLifecycleLock(list, func() error {
			if l.stopped.Load() || !r.authorized(plan) {
				return errors.New("Team launch authorization has been revoked")
			}
			executionStarted = true
			l.released.Store(true)
			sent, err := r.options.Release(ctx, id, text, func() bool { return !l.stopped.Load() })
			if err != nil {
				return err
			}
			if !sent {
				return fmt.Errorf("Failed to release %s", member.String("name"))
			}
			return nil
		})
	}, r.options.StopWorker)
	if err != nil {
		return result, err
	}
	r.mu.Lock()
	if r.closed || l.stopped.Load() {
		r.mu.Unlock()
		return result, errors.New("Team launch cancelled")
	}
	pollctx, cancel := context.WithCancel(r.ctx)
	l.cancel = cancel
	r.wg.Add(1)
	r.mu.Unlock()
	go r.poll(pollctx, l, members, memberIDs, createdAt)
	if _, err = r.options.SendLeader(ctx, plan.String("sessionId"), fmt.Sprintf("Approved team %s is running. Members and their shared tasks are ready. Continue coordinating the approved team; do not spawn these members again.", plan.String("teamName"))); err != nil {
		return result, err
	}
	return protocol.NewJSONObject(protocol.JSONField{Name: "memberIds", Value: raw(memberIDs)}), nil
}

func (r *Runtime) observeResult(l *launch, member protocol.JSONObject, id string, createdAt json.RawMessage, message protocol.JSONObject) {
	r.mu.Lock()
	if r.closed || l.stopped.Load() {
		r.mu.Unlock()
		return
	}
	r.wg.Add(1)
	r.mu.Unlock()
	go func() {
		defer r.wg.Done()
		teamName := l.plan.String("teamName")
		if err := r.options.Tasks.WithTeamLifecycleLock(teamstore.SanitizeName(teamName), func() error {
			team := r.options.Config.Read(teamName)
			if team == nil || number(team.Get("createdAt")) != number(createdAt) {
				return nil
			}
			entries := rows(team.Get("members"))
			for i, entry := range entries {
				if entry.String("sessionId") == id {
					entry.Set("isActive", raw(false))
					if !r.options.Connected(id) {
						entry.Set("terminated", raw(true))
					}
					entries[i] = entry
					team.Set("members", raw(entries))
					return r.options.Config.Write(teamName, *team)
				}
			}
			return nil
		}); err != nil {
			r.options.Log(fmt.Errorf("[TeamPlanRuntime] cannot update idle member: %w", err))
		}
		reason := "available"
		if !r.options.Connected(id) || string(message.Get("is_error")) == "true" {
			reason = "failed"
		}
		notice := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw("idle_notification")}, protocol.JSONField{Name: "from", Value: member.Get("name")}, protocol.JSONField{Name: "timestamp", Value: raw(r.options.Now().UTC().Format("2006-01-02T15:04:05.000Z"))}, protocol.JSONField{Name: "idleReason", Value: raw(reason)})
		if value := message.Get("result"); isString(value) {
			units, _ := protocol.DecodeJavaScriptJSONString(value)
			if len(units) > 1000 {
				units = units[:1000]
			}
			notice.Set("summary", protocol.QuoteJavaScriptUTF16(units))
		}
		if err := r.options.Mailbox.Write("team-lead", teamName, protocol.NewJSONObject(protocol.JSONField{Name: "from", Value: member.Get("name")}, protocol.JSONField{Name: "timestamp", Value: raw(r.options.Now().UTC().Format("2006-01-02T15:04:05.000Z"))}, protocol.JSONField{Name: "text", Value: raw(string(raw(notice)))})); err != nil {
			r.options.Log(err)
		}
	}()
}
func (r *Runtime) poll(ctx context.Context, l *launch, members []protocol.JSONObject, ids protocol.JSONObject, createdAt json.RawMessage) {
	defer r.wg.Done()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if l.stopped.Load() {
				return
			}
			if err := r.Poll(ctx, l.plan.String("planId"), members, ids, createdAt); err != nil {
				r.options.Log(fmt.Errorf("[TeamPlanRuntime] mailbox delivery failed: %w", err))
			}
		}
	}
}

// Poll is the real single-consumer inbox operation; the timer above owns calls.
// It marks only the immutable accepted batch, including Source legacy tuples.
func (r *Runtime) Poll(ctx context.Context, planID string, members []protocol.JSONObject, ids protocol.JSONObject, createdAt json.RawMessage) error {
	r.mu.Lock()
	l := r.launches[planID]
	r.mu.Unlock()
	if l == nil || l.stopped.Load() {
		return nil
	}
	plan := l.plan
	teamName := plan.String("teamName")
	team := r.options.Config.Read(teamName)
	if !r.options.Connected(plan.String("sessionId")) || team == nil || number(team.Get("createdAt")) != number(createdAt) {
		return r.Stop(ctx, planID)
	}
	for _, member := range members {
		id := ids.String(member.String("id"))
		if !r.options.Connected(id) {
			continue
		}
		unread, err := r.options.Mailbox.Unread(member.String("name"), teamName)
		if err != nil {
			return err
		}
		if len(unread) == 0 {
			continue
		}
		if err = r.options.Tasks.WithTeamLifecycleLock(teamstore.SanitizeName(teamName), func() error {
			team := r.options.Config.Read(teamName)
			if team == nil || number(team.Get("createdAt")) != number(createdAt) {
				return errors.New("Team generation has ended")
			}
			entries := rows(team.Get("members"))
			for i, entry := range entries {
				if entry.String("sessionId") == id {
					entry.Set("isActive", raw(true))
					entries[i] = entry
					team.Set("members", raw(entries))
					return r.options.Config.Write(teamName, *team)
				}
			}
			return nil
		}); err != nil {
			return err
		}
		texts := []string{}
		messageIDs, legacy := map[string]bool{}, map[string]bool{}
		for _, value := range unread {
			message := object(value)
			texts = append(texts, `<teammate-message teammate_id="`+message.String("from")+`">`+"\n"+message.String("text")+"\n</teammate-message>")
			if message.String("id") != "" {
				messageIDs[message.String("id")] = true
			} else {
				legacy[string(raw([]json.RawMessage{message.Get("from"), message.Get("timestamp"), message.Get("text")}))] = true
			}
		}
		sent, err := r.options.Release(ctx, id, strings.Join(texts, "\n"), nil)
		if err != nil {
			return err
		}
		if sent {
			if err = r.options.Mailbox.MarkMatching(member.String("name"), teamName, func(message protocol.JSONObject) bool {
				if message.String("id") != "" {
					return messageIDs[message.String("id")]
				}
				return legacy[string(raw([]json.RawMessage{message.Get("from"), message.Get("timestamp"), message.Get("text")}))]
			}); err != nil {
				return err
			}
		}
	}
	return nil
}
func (r *Runtime) Stop(ctx context.Context, id string) error {
	r.mu.Lock()
	l := r.launches[id]
	if l == nil {
		r.mu.Unlock()
		return nil
	}
	l.stopped.Store(true)
	if l.cancel != nil {
		l.cancel()
	}
	delete(r.launches, id)
	r.mu.Unlock()
	l.mu.Lock()
	children := append([]string{}, l.children...)
	remove := append([]func(){}, l.removeObservers...)
	l.mu.Unlock()
	for _, stop := range remove {
		if stop != nil {
			stop()
		}
	}
	var wg sync.WaitGroup
	for _, child := range children {
		wg.Add(1)
		go func(id string) { defer wg.Done(); _ = r.options.StopWorker(ctx, id) }(child)
	}
	wg.Wait()
	plan := l.plan
	teamName := plan.String("teamName")
	list := teamstore.SanitizeName(teamName)
	if err := r.options.Tasks.WithTeamLifecycleLock(list, func() error {
		team, err := r.generation(plan)
		if err != nil {
			return nil
		}
		members := []protocol.JSONObject{}
		for _, member := range rows(team.Get("members")) {
			matched := false
			for _, id := range children {
				matched = matched || member.String("sessionId") == id
			}
			if !matched {
				members = append(members, member)
			} else if l.released.Load() {
				member.Set("isActive", raw(false))
				member.Set("terminated", raw(true))
				members = append(members, member)
			}
		}
		team.Set("members", raw(members))
		return r.options.Config.Write(teamName, *team)
	}); err != nil {
		return err
	}
	if l.released.Load() {
		tasks, err := r.options.Tasks.List(list)
		if err != nil {
			return err
		}
		for _, task := range tasks {
			metadata := object(task.Get("metadata"))
			if metadata.String("teamPlanId") == plan.String("planId") && task.String("status") == "in_progress" {
				metadata.Set("teamRuntimeInterrupted", raw(true))
				if _, err = r.options.Tasks.UpdateTask(list, task.Get("id"), protocol.NewJSONObject(protocol.JSONField{Name: "metadata", Value: raw(metadata)})); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
func (r *Runtime) NotifyLeader(ctx context.Context, plan protocol.JSONObject, kind string) error {
	if kind == "approved" || !r.options.Connected(plan.String("sessionId")) {
		return nil
	}
	text := fmt.Sprintf("The user cancelled team plan %s. Do not launch its members. Wait for new user instructions.", plan.String("teamName"))
	if kind == "returned" {
		feedback := plan.Get("feedback")
		if feedback == nil || string(feedback) == "null" {
			feedback = raw("Revise the team proposal.")
		}
		text = fmt.Sprintf("The user returned team plan %s for revision. No members were launched. Feedback: %s", plan.String("teamName"), object(raw(map[string]json.RawMessage{"v": feedback})).String("v"))
	}
	_, err := r.options.SendLeader(ctx, plan.String("sessionId"), text)
	return err
}
func (r *Runtime) StopParent(ctx context.Context, parent string) error {
	stopOwned := func() {
		r.mu.Lock()
		ids := []string{}
		for id, l := range r.launches {
			if l.plan.String("sessionId") == parent {
				ids = append(ids, id)
			}
		}
		r.mu.Unlock()
		var wg sync.WaitGroup
		for _, id := range ids {
			wg.Add(1)
			go func(id string) { defer wg.Done(); _ = r.Stop(ctx, id) }(id)
		}
		wg.Wait()
	}
	r.mu.Lock()
	released := false
	for _, l := range r.launches {
		released = released || l.plan.String("sessionId") == parent && l.released.Load()
	}
	r.mu.Unlock()
	stopOwned()
	plan, err := r.options.Plans.FindForSession(parent)
	if err != nil {
		return err
	}
	if plan != nil && plan.String("state") == "launching" {
		_, err = r.options.Plans.Mutate(plan.String("teamName"), teamplan.CurrentIdentity(*plan), func(v protocol.JSONObject) (protocol.JSONObject, error) {
			state := "cancelled"
			if released {
				state = "interrupted"
			}
			v.Set("state", raw(state))
			status := object(v.Get("launch"))
			if !status.IsObject() {
				status = protocol.NewJSONObject()
			}
			status.Set("status", raw("failed"))
			status.Set("executionStarted", raw(released))
			status.Set("error", raw("The user stopped team startup."))
			v.Set("launch", raw(status))
			return v, nil
		})
		if err != nil {
			r.options.Log(fmt.Errorf("[TeamPlanRuntime] stopped plan changed concurrently: %w", err))
		}
	}
	stopOwned()
	return nil
}
func (r *Runtime) Close(ctx context.Context) error {
	r.mu.Lock()
	r.closed = true
	r.cancel()
	ids := []string{}
	for id := range r.launches {
		ids = append(ids, id)
	}
	r.mu.Unlock()
	var failures []error
	for _, id := range ids {
		if err := r.Stop(ctx, id); err != nil {
			failures = append(failures, err)
		}
	}
	done := make(chan struct{})
	go func() { r.wg.Wait(); close(done) }()
	select {
	case <-done:
		return errors.Join(failures...)
	case <-ctx.Done():
		return errors.Join(append(failures, context.Cause(ctx))...)
	}
}
