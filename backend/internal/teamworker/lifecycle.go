package teamworker

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamplan"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
	"strings"
	"time"
)

// PauseParent retains the approved launch and member transcripts. A user Stop
// never sends a model notice by itself and never consumes the crash budget.
func (r *Runtime) PauseParent(ctx context.Context, parent string) error {
	r.mu.Lock()
	owned := []*launch{}
	for _, l := range r.launches {
		if l.plan.String("sessionId") == parent && l.running.Load() && !l.stopped.Load() {
			owned = append(owned, l)
		}
	}
	r.mu.Unlock()
	for _, l := range owned {
		l.supervise.Lock()
		l.pausedAt = r.options.Now()
		l.resumeAllowedAt = time.Time{}
		ids := []string{}
		for _, w := range l.workers {
			w.retryAt = time.Time{}
			w.stoppedByUser = true
			w.wakeNoticePending = true
			ids = append(ids, w.id)
			if e := r.updateWorker(l, w, func(entry protocol.JSONObject) { entry.Set("isActive", raw(false)); entry.Set("terminated", raw(true)) }); e != nil {
				l.supervise.Unlock()
				return e
			}
		}
		l.mu.Lock()
		startups := []workerStartup{}
		for _, startup := range l.startups {
			startup.cancel()
			startups = append(startups, startup)
		}
		l.mu.Unlock()
		l.supervise.Unlock()
		for _, startup := range startups {
			select {
			case <-startup.done:
			case <-ctx.Done():
				return context.Cause(ctx)
			}
		}
		for _, id := range ids {
			if e := r.options.StopWorker(ctx, id); e != nil {
				return e
			}
		}
	}
	return r.stopUnreleasedParent(ctx, parent)
}
func (r *Runtime) stopUnreleasedParent(ctx context.Context, parent string) error {
	r.mu.Lock()
	ids := []string{}
	for id, l := range r.launches {
		if l.plan.String("sessionId") == parent && !l.running.Load() {
			ids = append(ids, id)
		}
	}
	r.mu.Unlock()
	released := false
	for _, id := range ids {
		r.mu.Lock()
		l := r.launches[id]
		r.mu.Unlock()
		if l != nil {
			released = released || l.released.Load()
		}
		if e := r.Stop(ctx, id); e != nil {
			return e
		}
	}
	plan, e := r.options.Plans.FindForSession(parent)
	if e != nil {
		return e
	}
	if plan != nil && plan.String("state") == "launching" {
		_, e = r.options.Plans.Mutate(plan.String("teamName"), teamplan.CurrentIdentity(*plan), func(v protocol.JSONObject) (protocol.JSONObject, error) {
			state := "cancelled"
			if released {
				state = "interrupted"
			}
			v.Set("state", raw(state))
			status := object(v.Get("launch"))
			status.Set("status", raw("failed"))
			status.Set("executionStarted", raw(released))
			status.Set("error", raw("The user stopped team startup."))
			v.Set("launch", raw(status))
			return v, nil
		})
	}
	return e
}
func (r *Runtime) NoteLeadUserMessage(ctx context.Context, parent string) error {
	r.mu.Lock()
	owned := []*launch{}
	for _, l := range r.launches {
		if l.plan.String("sessionId") == parent && !l.stopped.Load() {
			owned = append(owned, l)
		}
	}
	r.mu.Unlock()
	for _, l := range owned {
		l.supervise.Lock()
		if !l.pausedAt.IsZero() {
			l.resumeAllowedAt = r.options.Now()
		}
		waiting := []*workerRuntime{}
		for _, member := range rows(object(l.plan.Get("approvedSnapshot")).Get("members")) {
			w := l.workers[member.String("id")]
			if w != nil && w.released && w.wakeNoticePending {
				waiting = append(waiting, w)
				w.wakeNoticePending = false
			}
		}
		if len(waiting) == 0 {
			l.supervise.Unlock()
			continue
		}
		tasks, e := r.options.Tasks.List(teamstore.SanitizeName(l.plan.String("teamName")))
		if e != nil {
			l.supervise.Unlock()
			return e
		}
		unresolved := map[string]bool{}
		for _, task := range tasks {
			if task.String("status") != "completed" {
				unresolved[task.String("id")] = true
			}
		}
		lines := []string{}
		stopped := false
		for _, w := range waiting {
			stopped = stopped || w.stoppedByUser
			label := w.member.String("name")
			if w.failure != "" {
				label += " (stopped on an error: " + w.failure + ")"
			}
			open := []protocol.JSONObject{}
			for _, task := range tasks {
				if task.String("owner") == w.member.String("name") && task.String("status") != "completed" {
					open = append(open, task)
				}
			}
			if w.brief != "" && w.failure == "" {
				ready := readyTasksOf(w.member.String("name"), tasks)
				labels := []string{}
				for _, task := range ready {
					labels = append(labels, "#"+task.String("id")+" "+task.String("subject"))
				}
				if len(ready) > 0 {
					lines = append(lines, "- "+label+": not started yet; "+strings.Join(labels, "; ")+" is ready for it, so message it to start")
					continue
				}
				blockers := []string{}
				seen := map[string]bool{}
				for _, task := range open {
					for _, dep := range stringsIn(task.Get("blockedBy")) {
						if unresolved[dep] && !seen[dep] {
							seen[dep] = true
							blockers = append(blockers, "#"+dep)
						}
					}
				}
				if len(blockers) > 0 {
					verb := "are"
					if len(blockers) == 1 {
						verb = "is"
					}
					lines = append(lines, "- "+label+": not started yet; it starts by itself once "+strings.Join(blockers, ", ")+" "+verb+" completed, and messages to it wait until then")
					continue
				}
				lines = append(lines, "- "+label+": not started yet; no unfinished task")
				continue
			}
			labels := []string{}
			for _, task := range open {
				labels = append(labels, "#"+task.String("id")+" "+task.String("subject")+" ("+task.String("status")+")")
			}
			if len(labels) == 0 {
				lines = append(lines, "- "+label+": no unfinished task")
			} else {
				lines = append(lines, "- "+label+": "+strings.Join(labels, "; "))
			}
		}
		header := fmt.Sprintf("[Team runtime notice] These members of your team %s stopped on an error and keep their work so far:", l.plan.String("teamName"))
		if stopped {
			header = fmt.Sprintf("[Team runtime notice] The user's Stop also stopped your team %s. These members are stopped and keep their work so far:", l.plan.String("teamName"))
		}
		text := header + "\n" + strings.Join(lines, "\n") + "\nA stopped member does nothing until it is messaged: if the work should go on, SendMessage each member that still has work, and it resumes from its saved conversation where it left off. Do not spawn replacements or redo their tasks. If a woken member fails again with the same error (a usage limit, billing, the network), tell the user instead of retrying. If the user wants the team to stay stopped, leave them."
		l.supervise.Unlock()
		if _, e = r.options.SendLeader(ctx, parent, text); e != nil {
			return e
		}
	}
	return nil
}
func (r *Runtime) HasPendingWork(parent string) bool {
	r.mu.Lock()
	owned := []*launch{}
	for _, l := range r.launches {
		if l.plan.String("sessionId") == parent && !l.stopped.Load() {
			owned = append(owned, l)
		}
	}
	r.mu.Unlock()
	for _, l := range owned {
		l.supervise.Lock()
		if !l.pausedAt.IsZero() {
			l.supervise.Unlock()
			continue
		}
		if !l.running.Load() {
			l.supervise.Unlock()
			return true
		}
		team, e := r.generation(l.plan)
		if e != nil {
			l.supervise.Unlock()
			continue
		}
		entries := rows(team.Get("members"))
		pending := false
		for _, w := range l.workers {
			if w.starting || !w.retryAt.IsZero() {
				pending = true
			}
			for _, entry := range entries {
				if r.options.Connected(w.id) && entry.String("sessionId") == w.id && string(entry.Get("isActive")) == "true" {
					pending = true
				}
			}
		}
		l.supervise.Unlock()
		if pending {
			return true
		}
	}
	return false
}
func (r *Runtime) Rehydrate(ctx context.Context, plan protocol.JSONObject) (bool, error) {
	if plan.String("state") != "running" || !object(plan.Get("approvedSnapshot")).IsObject() {
		return false, nil
	}
	r.mu.Lock()
	exists := r.launches[plan.String("planId")] != nil
	r.mu.Unlock()
	if exists {
		return true, nil
	}
	team, e := r.generation(plan)
	if e != nil {
		return false, nil
	}
	members := rows(object(plan.Get("approvedSnapshot")).Get("members"))
	l := &launch{plan: plan.Clone(), createdAt: append(json.RawMessage(nil), team.Get("createdAt")...), workers: map[string]*workerRuntime{}}
	l.permissionMode, e = r.options.Validation.PermissionMode(ctx, plan.String("sessionId"))
	if e != nil {
		return false, e
	}
	ids := object(object(plan.Get("launch")).Get("memberIds"))
	var mapping map[string]string
	for _, member := range members {
		id := ids.String(member.String("id"))
		entry := protocol.JSONObject{}
		for _, candidate := range rows(team.Get("members")) {
			if id == "" && candidate.String("planMemberId") == member.String("id") && candidate.String("backendType") == "process" {
				id = candidate.String("sessionId")
			}
			if id != "" && candidate.String("sessionId") == id {
				entry = candidate
			}
		}
		if id == "" || !entry.IsObject() {
			continue
		}
		w := &workerRuntime{member: member.Clone(), id: id, released: true, woken: map[string]bool{}, lastResultAt: r.options.Now().Add(-r.options.DependencyWakeDelay)}
		if string(entry.Get("awaitingDependencies")) == "true" {
			if mapping == nil {
				mapping = map[string]string{}
				tasks, e := r.options.Tasks.List(teamstore.SanitizeName(plan.String("teamName")))
				if e != nil {
					return false, e
				}
				for _, task := range tasks {
					meta := object(task.Get("metadata"))
					if meta.String("teamPlanId") == plan.String("planId") && meta.String("teamPlanTaskId") != "" {
						mapping[meta.String("teamPlanTaskId")] = task.String("id")
					}
				}
			}
			w.brief = memberBrief(member, members, rows(object(plan.Get("approvedSnapshot")).Get("tasks")), mapping)
		}
		l.workers[member.String("id")] = w
		l.children = append(l.children, id)
	}
	if len(l.workers) == 0 {
		return false, nil
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return false, context.Canceled
	}
	if r.launches[plan.String("planId")] != nil {
		r.mu.Unlock()
		return true, nil
	}
	l.running.Store(true)
	l.released.Store(true)
	r.launches[plan.String("planId")] = l
	pollctx, cancel := context.WithCancel(r.ctx)
	l.cancel = cancel
	r.wg.Add(1)
	r.mu.Unlock()
	for _, w := range l.workers {
		if !r.options.Connected(w.id) {
			_ = r.updateWorker(l, w, func(entry protocol.JSONObject) { entry.Set("isActive", raw(false)); entry.Set("terminated", raw(true)) })
		}
	}
	go r.poll(pollctx, l, members, ids, l.createdAt)
	return true, nil
}
func (r *Runtime) LeadStarted(ctx context.Context, parent string) error {
	plans, e := r.options.Plans.HistoryForSession(parent)
	if e != nil {
		return e
	}
	for _, plan := range plans {
		if _, e = r.Rehydrate(ctx, plan); e != nil {
			return e
		}
	}
	r.mu.Lock()
	owned := []*launch{}
	for _, l := range r.launches {
		if l.plan.String("sessionId") == parent && l.running.Load() && !l.stopped.Load() {
			owned = append(owned, l)
		}
	}
	r.mu.Unlock()
	for _, l := range owned {
		if e = r.options.Snapshot(ctx, parent, l.plan.String("teamName"), l.createdAt); e != nil {
			return e
		}
	}
	return nil
}
