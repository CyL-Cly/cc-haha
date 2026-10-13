package teamworker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
)

// All worker state is serialized by launch.supervise. Process startup runs
// outside that mutex so a slow provider barrier cannot block another member.
type workerRuntime struct {
	member                                                                  protocol.JSONObject
	id, brief, failure                                                      string
	released, starting, stoppedByUser, wakeNoticePending, restartsExhausted bool
	attempts                                                                int
	retryAt, lastResultAt, shutdownRequestedAt                              time.Time
	restarts                                                                []time.Time
	woken                                                                   map[string]bool
	removeObserver                                                          func()
}

var nonTransientFailure = regexp.MustCompile(`(?i)prompt is too long|context (length|window)|maximum context|credit balance|not logged in|invalid api key|invalid x-api-key|authentication|unauthori[sz]ed|forbidden|\b40[13]\b|billing|payment required|\b402\b|extra usage is required|invalid_request_error|model[_ ]not[_ ]found|no such model|unsupported model|max(imum)? turns|request was aborted|interrupted by user`)
var transientFailure = regexp.MustCompile(`(?i)stream_truncated|stream_error|ended without (a )?(finish_reason|message_stop)|provider stream ended|stream ended without|without receiving any events|overloaded|repeated 529|\b5\d\d\b|server_error|api_error|internal server error|bad gateway|service unavailable|gateway time-?out|timed? ?out|timeout|stream idle|stalled|econnreset|econnrefused|etimedout|epipe|socket hang up|fetch failed|connection (error|reset|closed)|network|terminated|rate.?limit|\b429\b|too many requests`)

func IsTransientTurnFailure(text string) bool {
	return strings.TrimSpace(text) != "" && !nonTransientFailure.MatchString(text) && transientFailure.MatchString(text)
}
func capText(text string, limit int) string {
	text = strings.Map(func(r rune) rune {
		if r <= 8 || r == 11 || r == 12 || r >= 14 && r <= 31 {
			return -1
		}
		return r
	}, text)
	text = protocol.TrimJavaScriptString(text)
	units, _ := protocol.DecodeJavaScriptJSONString(raw(text))
	if len(units) > limit {
		units = append(units[:limit], uint16('…'))
		return object(raw(map[string]json.RawMessage{"v": protocol.QuoteJavaScriptUTF16(units)})).String("v")
	}
	return text
}
func firstLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if protocol.TrimJavaScriptString(line) != "" {
			return capText(line, 200)
		}
	}
	return ""
}
func FormatTeammateAutoContinuePrompt(reason string, attempt, max int) string {
	if reason == "" {
		reason = "unknown error"
	}
	return fmt.Sprintf("[Team runtime notice] Your previous turn stopped early because of a transient model/API error (%s; automatic retry %d of %d).\nContinue your assigned work from where you left off. Do not redo steps that already finished. If your work is already complete, report the result to the team lead with SendMessage and mark your task completed.", reason, attempt, max)
}
func memberBrief(member protocol.JSONObject, members, tasks []protocol.JSONObject, mapping map[string]string) string {
	assigned := []protocol.JSONObject{}
	handoff := false
	for _, task := range tasks {
		if task.String("ownerId") != member.String("id") {
			continue
		}
		next := task.Clone()
		next.Set("id", raw(mapping[task.String("id")]))
		deps := []string{}
		for _, id := range stringsIn(task.Get("dependencies")) {
			deps = append(deps, mapping[id])
		}
		next.Set("dependencies", raw(deps))
		recipients := []string{}
		seen := map[string]bool{}
		for _, other := range tasks {
			for _, dep := range stringsIn(other.Get("dependencies")) {
				if dep != task.String("id") {
					continue
				}
				name := "team-lead"
				for _, candidate := range members {
					if candidate.String("id") == other.String("ownerId") {
						name = candidate.String("name")
						break
					}
				}
				if name != member.String("name") && !seen[name] {
					seen[name] = true
					recipients = append(recipients, name)
				}
			}
		}
		if len(recipients) > 0 {
			next.Set("handOffTo", raw(recipients))
			handoff = true
		}
		assigned = append(assigned, next)
	}
	prefix := ""
	if initial := object(member.Get("agentSnapshot")).String("initialPrompt"); initial != "" {
		prefix = initial + "\n"
	}
	text := prefix + member.String("prompt") + "\n\nApproved shared tasks (use TaskGet/TaskUpdate; start a task only once all of its dependencies are completed):\n" + string(raw(assigned))
	if handoff {
		text += "\n\nWhen you finish a task that has handOffTo: first SendMessage its result to each member listed there, then mark it completed. Their work starts from your message the moment you complete the task."
	}
	return text
}
func launchNotice(team string, members []protocol.JSONObject, waiting map[string]bool) string {
	names := []string{}
	for _, member := range members {
		if waiting[member.String("id")] {
			names = append(names, member.String("name"))
		}
	}
	note := ""
	if len(names) > 0 {
		note = " Every task of " + strings.Join(names, ", ") + " waits on other tasks: such a member gets its instructions automatically once one of its tasks is ready. Messages sent to it before that wait in its inbox and arrive with those instructions. Each member is told to send its result to the members whose tasks depend on it before completing, so forward a result yourself only when a member says one is missing."
	}
	return fmt.Sprintf("Approved team %s is running. Members and their shared tasks are ready.%s Continue coordinating the approved team; do not spawn these members again. Members report back automatically when they finish or fail; you do not need to poll or wait in a loop.", team, note)
}
func readyTasksOf(name string, tasks []protocol.JSONObject) []protocol.JSONObject {
	unresolved := map[string]bool{}
	for _, task := range tasks {
		if task.String("status") != "completed" {
			unresolved[task.String("id")] = true
		}
	}
	ready := []protocol.JSONObject{}
	for _, task := range tasks {
		if task.String("owner") != name || task.String("status") == "completed" {
			continue
		}
		blocked := false
		for _, dep := range stringsIn(task.Get("blockedBy")) {
			blocked = blocked || unresolved[dep]
		}
		if !blocked {
			ready = append(ready, task)
		}
	}
	return ready
}
func waitsForDependencies(name string, tasks []protocol.JSONObject) bool {
	for _, task := range tasks {
		if task.String("owner") == name && task.String("status") != "completed" {
			return len(readyTasksOf(name, tasks)) == 0
		}
	}
	return false
}
func (r *Runtime) updateWorker(l *launch, w *workerRuntime, update func(protocol.JSONObject)) error {
	return r.options.Tasks.WithTeamLifecycleLock(teamstore.SanitizeName(l.plan.String("teamName")), func() error {
		team, e := r.generation(l.plan)
		if e != nil || number(team.Get("createdAt")) != number(l.createdAt) {
			return e
		}
		entries := rows(team.Get("members"))
		for i, entry := range entries {
			if entry.String("sessionId") == w.id {
				update(entry)
				entries[i] = entry
				team.Set("members", raw(entries))
				return r.options.Config.Write(l.plan.String("teamName"), *team)
			}
		}
		return nil
	})
}
func clearEntryFailure(entry protocol.JSONObject) {
	entry.Delete("lastError")
	entry.Delete("autoRetry")
}
func (r *Runtime) notifyLead(ctx context.Context, l *launch, w *workerRuntime, reason, result, failure string) error {
	stamp := r.options.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	notice := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw("idle_notification")}, protocol.JSONField{Name: "from", Value: w.member.Get("name")}, protocol.JSONField{Name: "timestamp", Value: raw(stamp)}, protocol.JSONField{Name: "idleReason", Value: raw(reason)})
	if result != "" {
		notice.Set("result", raw(capText(result, 4000)))
	}
	if failure != "" {
		notice.Set("failureReason", raw(failure))
	}
	message := protocol.NewJSONObject(protocol.JSONField{Name: "from", Value: w.member.Get("name")}, protocol.JSONField{Name: "timestamp", Value: raw(stamp)}, protocol.JSONField{Name: "text", Value: raw(string(raw(notice)))})
	e := r.options.Mailbox.Write("team-lead", l.plan.String("teamName"), message)
	if e == nil {
		return nil
	}
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-timer.C:
	}
	return r.options.Mailbox.Write("team-lead", l.plan.String("teamName"), message)
}
func (r *Runtime) notifyFailure(ctx context.Context, l *launch, w *workerRuntime, reason string) error {
	w.failure = reason
	w.wakeNoticePending = true
	return r.notifyLead(ctx, l, w, "failed", "", reason)
}
func clearWorkerFailure(w *workerRuntime) { w.failure = ""; w.wakeNoticePending = false }
func (r *Runtime) approvedShutdown(ctx context.Context, l *launch, w *workerRuntime) bool {
	since := w.shutdownRequestedAt
	if since.IsZero() {
		return false
	}
	w.shutdownRequestedAt = time.Time{}
	check := func() bool {
		messages := r.options.Mailbox.ReadHistory("team-lead", l.plan.String("teamName"))
		for _, message := range rows(messages) {
			at, e := time.Parse(time.RFC3339Nano, message.String("timestamp"))
			if e == nil && message.String("from") == w.member.String("name") && !at.Before(since) && object([]byte(message.String("text"))).String("type") == "shutdown_approved" {
				return true
			}
		}
		return false
	}
	if check() {
		return true
	}
	timer := time.NewTimer(300 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
	}
	return check()
}
func (r *Runtime) handleWorkerResult(ctx context.Context, l *launch, w *workerRuntime, message protocol.JSONObject) error {
	w.lastResultAt = r.options.Now()
	alive := r.options.Connected(w.id)
	text := message.String("result")
	failed := !alive || string(message.Get("is_error")) == "true"
	if !l.pausedAt.IsZero() {
		return r.updateWorker(l, w, func(e protocol.JSONObject) {
			e.Set("isActive", raw(false))
			if !alive {
				e.Set("terminated", raw(true))
			}
		})
	}
	if !failed || !alive && r.approvedShutdown(ctx, l, w) {
		w.attempts = 0
		w.retryAt = time.Time{}
		clearWorkerFailure(w)
		if e := r.updateWorker(l, w, func(e protocol.JSONObject) {
			e.Set("isActive", raw(false))
			if !alive {
				e.Set("terminated", raw(true))
			}
			clearEntryFailure(e)
		}); e != nil {
			return e
		}
		if failed {
			return nil
		}
		return r.notifyLead(ctx, l, w, "available", text, "")
	}
	reason := firstLine(text)
	if reason == "" {
		reason = "The member process exited"
	}
	if alive && IsTransientTurnFailure(text) && w.attempts < len(r.options.AutoContinueDelays) {
		w.attempts++
		w.retryAt = r.options.Now().Add(r.options.AutoContinueDelays[w.attempts-1])
		return r.updateWorker(l, w, func(e protocol.JSONObject) {
			e.Set("isActive", raw(false))
			e.Set("lastError", raw(reason))
			e.Set("autoRetry", raw(map[string]any{"attempt": w.attempts, "max": len(r.options.AutoContinueDelays), "nextAt": w.retryAt.UnixMilli()}))
		})
	}
	w.retryAt = time.Time{}
	if e := r.updateWorker(l, w, func(e protocol.JSONObject) {
		e.Delete("autoRetry")
		e.Set("isActive", raw(false))
		e.Set("lastError", raw(reason))
		if !alive {
			e.Set("terminated", raw(true))
		}
	}); e != nil {
		return e
	}
	failure := reason
	if !alive {
		failure = fmt.Sprintf("%s's process exited (%s). Messaging it restarts it from its saved conversation.", w.member.String("name"), reason)
	} else if IsTransientTurnFailure(text) {
		failure = fmt.Sprintf("%s (automatic retries exhausted; message %s to continue)", reason, w.member.String("name"))
	}
	return r.notifyFailure(ctx, l, w, failure)
}
func shutdownRequest(message protocol.JSONObject) bool {
	return object([]byte(message.String("text"))).String("type") == "shutdown_request"
}
func (r *Runtime) deliver(ctx context.Context, l *launch, w *workerRuntime, messages []json.RawMessage) error {
	w.retryAt = time.Time{}
	w.attempts = 0
	if e := r.updateWorker(l, w, func(e protocol.JSONObject) {
		clearEntryFailure(e)
		e.Set("isActive", raw(true))
		e.Set("terminated", raw(false))
	}); e != nil {
		return e
	}
	onlyShutdown := true
	for _, data := range messages {
		if !shutdownRequest(object(data)) {
			onlyShutdown = false
		} else {
			w.shutdownRequestedAt = r.options.Now()
		}
	}
	starts := w.brief != "" && !onlyShutdown
	formatted := make([]protocol.JSONObject, 0, len(messages))
	for _, data := range messages {
		formatted = append(formatted, object(data))
	}
	text := teamstore.FormatMessages(formatted)
	if starts {
		text = w.brief + "\n\n" + text
	}
	sent, e := r.options.Release(ctx, w.id, text, nil)
	if e != nil || !sent {
		_ = r.updateWorker(l, w, func(e protocol.JSONObject) { e.Set("isActive", raw(false)) })
		return e
	}
	clearWorkerFailure(w)
	if starts {
		w.brief = ""
		if e = r.updateWorker(l, w, func(e protocol.JSONObject) { e.Delete("awaitingDependencies") }); e != nil {
			return e
		}
	}
	// Accepted input acknowledges only its captured identities. Synthetic task-list
	// messages have no live match; arrivals while the input was accepted stay unread.
	identities := map[string]bool{}
	for _, data := range messages {
		identities[teamstore.MailboxIdentity(object(data))] = true
	}
	return r.options.Mailbox.MarkMatching(w.member.String("name"), l.plan.String("teamName"), func(message protocol.JSONObject) bool { return identities[teamstore.MailboxIdentity(message)] })
}
func (r *Runtime) startRequest(l *launch, w *workerRuntime, resume bool) WorkerRequest {
	member := w.member
	snapshot := object(member.Get("agentSnapshot"))
	runtime := object(member.Get("runtime"))
	var tools *[]string
	if snapshot.Get("tools") != nil {
		names := AddEssentialTools(stringsIn(snapshot.Get("tools")))
		tools = &names
	}
	var provider *string
	if runtime.String("providerId") != "claude-official" {
		v := runtime.String("providerId")
		provider = &v
	}
	definition := snapshot.Clone()
	definition.Delete("initialPrompt")
	definition.Set("effort", runtime.Get("effortLevel"))
	return WorkerRequest{ID: w.id, WorkDir: l.plan.String("workDir"), Model: runtime.String("modelId"), Effort: runtime.String("effortLevel"), PermissionMode: l.permissionMode, ProviderID: provider, Resume: resume, Worker: Start{ParentSessionID: l.plan.String("sessionId"), TeamName: l.plan.String("teamName"), MemberID: member.String("id"), Name: member.String("name"), SystemPrompt: snapshot.String("systemPrompt"), Tools: tools, AgentDefinition: definition}}
}
func (r *Runtime) restart(ctx context.Context, l *launch, w *workerRuntime, user bool) error {
	if w.starting {
		return nil
	}
	now := r.options.Now()
	recent := []time.Time{}
	for _, at := range w.restarts {
		if now.Sub(at) < 10*time.Minute {
			recent = append(recent, at)
		}
	}
	w.restarts = recent
	resuming := w.stoppedByUser
	w.stoppedByUser = false
	if !user && !resuming && (w.restartsExhausted || len(w.restarts) >= 3) {
		if !w.restartsExhausted {
			w.restartsExhausted = true
			_ = r.updateWorker(l, w, func(e protocol.JSONObject) {
				e.Set("isActive", raw(false))
				e.Set("terminated", raw(true))
				e.Set("lastError", raw("Stopped restarting after repeated exits. A message from the user restarts it."))
			})
			return r.notifyFailure(ctx, l, w, fmt.Sprintf("%s exited %d times in 10 minutes; automatic restarts are paused", w.member.String("name"), len(w.restarts)))
		}
		return nil
	}
	if !resuming {
		w.restarts = append(w.restarts, now)
	}
	if user {
		w.restartsExhausted = false
	}
	w.starting = true
	startupCtx, startupCancel := context.WithCancel(r.ctx)
	done := make(chan struct{})
	r.mu.Lock()
	if r.closed || l.stopped.Load() {
		r.mu.Unlock()
		startupCancel()
		w.starting = false
		return nil
	}
	r.wg.Add(1)
	l.mu.Lock()
	if l.startups == nil {
		l.startups = map[string]workerStartup{}
	}
	l.startups[w.id] = workerStartup{startupCancel, done}
	l.mu.Unlock()
	r.mu.Unlock()
	request := r.startRequest(l, w, true)
	go func() {
		defer r.wg.Done()
		defer startupCancel()
		if mode, e := r.options.Validation.PermissionMode(startupCtx, l.plan.String("sessionId")); e == nil {
			request.PermissionMode = mode
		}
		actual, e := r.options.Prepare(startupCtx, request)
		if e == nil && actual != w.id {
			_ = r.options.StopWorker(context.Background(), actual)
			e = errors.New("Team prepared session identity changed")
		}
		close(done)
		l.mu.Lock()
		if current, ok := l.startups[w.id]; ok && current.done == done {
			delete(l.startups, w.id)
		}
		l.mu.Unlock()
		if l.stopped.Load() {
			return
		}
		l.supervise.Lock()
		defer l.supervise.Unlock()
		w.starting = false
		if l.stopped.Load() || w.stoppedByUser {
			if e == nil {
				_ = r.options.StopWorker(context.Background(), w.id)
			}
			return
		}
		if e != nil {
			r.options.Log(e)
			_ = r.updateWorker(l, w, func(entry protocol.JSONObject) {
				entry.Set("isActive", raw(false))
				entry.Set("terminated", raw(true))
				entry.Set("lastError", raw(firstLine("Restart failed: "+e.Error())))
			})
			return
		}
		if w.removeObserver != nil {
			w.removeObserver()
		}
		r.mu.Lock()
		if r.closed || l.stopped.Load() {
			r.mu.Unlock()
			_ = r.options.StopWorker(context.Background(), w.id)
			return
		}
		remove, e := r.options.Observe(w.id, func(message protocol.JSONObject) {
			if message.String("type") == "result" {
				r.observeResult(l, w.member, w.id, l.createdAt, message)
			}
		})
		if e != nil {
			r.mu.Unlock()
			r.options.Log(e)
			return
		}
		w.removeObserver = remove
		l.mu.Lock()
		l.removeObservers = append(l.removeObservers, remove)
		l.mu.Unlock()
		r.mu.Unlock()
		_ = r.updateWorker(l, w, func(entry protocol.JSONObject) { clearEntryFailure(entry); entry.Set("terminated", raw(false)) })
	}()
	return nil
}
func (r *Runtime) readyWake(ctx context.Context, l *launch, w *workerRuntime, tasks []protocol.JSONObject, held []json.RawMessage, entry protocol.JSONObject) error {
	ready := readyTasksOf(w.member.String("name"), tasks)
	if w.brief != "" {
		if len(ready) == 0 {
			return nil
		}
		if !r.options.Connected(w.id) {
			return r.restart(ctx, l, w, false)
		}
		labels := []string{}
		upstream := []string{}
		seen := map[string]bool{}
		for _, task := range ready {
			w.woken[task.String("id")] = true
			labels = append(labels, "#"+task.String("id")+" "+task.String("subject"))
			for _, dep := range stringsIn(task.Get("blockedBy")) {
				for _, other := range tasks {
					if other.String("id") == dep {
						owner := other.String("owner")
						isMember := false
						for _, peer := range l.workers {
							isMember = isMember || peer.member.String("name") == owner
						}
						if owner != "" && owner != w.member.String("name") && isMember && !seen[owner] {
							seen[owner] = true
							upstream = append(upstream, owner)
						}
					}
				}
			}
		}
		missing := []string{}
		for _, owner := range upstream {
			present := false
			for _, data := range held {
				present = present || object(data).String("from") == owner
			}
			if !present {
				missing = append(missing, owner)
			}
		}
		note := ""
		if len(missing) > 0 {
			pronoun := "their"
			if len(missing) == 1 {
				pronoun = "its"
			}
			note = "\nNothing has reached you from " + strings.Join(missing, ", ") + " yet. If you need " + pronoun + " result, ask with SendMessage before you rely on it."
		}
		messages := append([]json.RawMessage{}, held...)
		messages = append(messages, raw(map[string]any{"from": "task-list", "text": "Ready now (dependencies complete): " + strings.Join(labels, "; ") + note, "timestamp": r.options.Now().UTC().Format("2006-01-02T15:04:05.000Z"), "read": false}))
		return r.deliver(ctx, l, w, messages)
	}
	if !entry.IsObject() || string(entry.Get("isActive")) == "true" || !w.retryAt.IsZero() || !r.options.Connected(w.id) || w.lastResultAt.IsZero() || r.options.Now().Sub(w.lastResultAt) < r.options.DependencyWakeDelay {
		return nil
	}
	for _, task := range ready {
		if task.String("status") != "pending" || w.woken[task.String("id")] || len(stringsIn(task.Get("blockedBy"))) == 0 {
			continue
		}
		w.woken[task.String("id")] = true
		return r.deliver(ctx, l, w, []json.RawMessage{raw(map[string]any{"from": "task-list", "text": fmt.Sprintf("Task #%s assigned to you is now ready because its dependencies are complete: %s\nStart it now with TaskUpdate (status in_progress), and mark it completed when done.", task.String("id"), task.String("subject")), "timestamp": r.options.Now().UTC().Format("2006-01-02T15:04:05.000Z"), "read": false})})
	}
	return nil
}
func (r *Runtime) superviseLaunch(ctx context.Context, l *launch) error {
	if l.stopped.Load() {
		return nil
	}
	team, e := r.generation(l.plan)
	if e != nil || number(team.Get("createdAt")) != number(l.createdAt) {
		return r.Stop(ctx, l.plan.String("planId"))
	}
	leadAlive := r.options.Connected(l.plan.String("sessionId"))
	var tasks []protocol.JSONObject
	for _, member := range rows(object(l.plan.Get("approvedSnapshot")).Get("members")) {
		w := l.workers[member.String("id")]
		if w == nil || !w.released || w.starting {
			continue
		}
		entry := protocol.JSONObject{}
		for _, candidate := range rows(team.Get("members")) {
			if candidate.String("sessionId") == w.id {
				entry = candidate
				break
			}
		}
		alive := r.options.Connected(w.id)
		if entry.IsObject() && string(entry.Get("terminated")) != "true" && !alive {
			_ = r.updateWorker(l, w, func(e protocol.JSONObject) { e.Set("isActive", raw(false)); e.Set("terminated", raw(true)) })
		}
		messages, e := r.options.Mailbox.Unread(w.member.String("name"), l.plan.String("teamName"))
		if e != nil {
			return e
		}
		if !l.pausedAt.IsZero() {
			resume := false
			for _, data := range messages {
				message := object(data)
				at, e := time.Parse(time.RFC3339Nano, message.String("timestamp"))
				if e != nil {
					continue
				}
				resume = resume || message.String("from") == "user" && !at.Before(l.pausedAt) || message.String("from") == "team-lead" && !l.resumeAllowedAt.IsZero() && !at.Before(l.resumeAllowedAt)
			}
			if !resume {
				continue
			}
			l.pausedAt = time.Time{}
			l.resumeAllowedAt = time.Time{}
		}
		directUser, shutdown := false, false
		for _, data := range messages {
			message := object(data)
			directUser = directUser || message.String("from") == "user"
			shutdown = shutdown || shutdownRequest(message)
		}
		held := w.brief != "" && !directUser && !shutdown
		if len(messages) == 0 || held {
			if !w.retryAt.IsZero() && !r.options.Now().Before(w.retryAt) && alive && !held {
				w.retryAt = time.Time{}
				if string(entry.Get("isActive")) != "true" {
					reason := entry.String("lastError")
					_ = r.updateWorker(l, w, func(e protocol.JSONObject) { e.Set("isActive", raw(true)) })
					text := FormatTeammateAutoContinuePrompt(reason, w.attempts, len(r.options.AutoContinueDelays))
					sent, e := r.options.Release(ctx, w.id, text, nil)
					if e != nil {
						return e
					}
					if !sent {
						_ = r.updateWorker(l, w, func(e protocol.JSONObject) { e.Delete("autoRetry"); e.Set("isActive", raw(false)) })
						if e = r.notifyFailure(ctx, l, w, fmt.Sprintf("%s (could not continue automatically; message %s to retry)", reason, w.member.String("name"))); e != nil {
							return e
						}
					}
				}
				continue
			}
			if !leadAlive {
				continue
			}
			if tasks == nil {
				tasks, e = r.options.Tasks.List(teamstore.SanitizeName(l.plan.String("teamName")))
				if e != nil {
					return e
				}
			}
			if e = r.readyWake(ctx, l, w, tasks, messages, entry); e != nil {
				return e
			}
			continue
		}
		if !alive {
			if !leadAlive && !directUser {
				continue
			}
			if e = r.restart(ctx, l, w, directUser); e != nil {
				return e
			}
			continue
		}
		if e = r.deliver(ctx, l, w, messages); e != nil {
			return e
		}
	}
	return nil
}
