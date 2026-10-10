package teamreader

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
)

func (s *Service) MarkArchiveDeleted(ctx context.Context, name, session, id string) error {
	if session == "" {
		return nil
	}
	messages, historyError := s.readTeamHistory(ctx, session)
	lifecycle, _ := s.options.Tasks.ReadLifecycle(teamstore.SanitizeName(name))
	path := s.ArchivePath(session)
	lock := s.archiveLock(path)
	lock.Lock()
	defer lock.Unlock()
	current := s.ReadArchive(session)
	entry := s.SelectArchiveEntry(current, protocol.NewJSONObject(field("teamName", name), field("incarnationId", id)))
	if current == nil || entry == nil {
		return nil
	}
	latest := lastSnapshot(*entry)
	if !latest.IsObject() {
		return nil
	}
	snapshots := rows(entry.Get("snapshots"))
	base := latest
	if jsTruthy(latest.Get("deletedAt")) {
		base = protocol.JSONObject{}
		for i := len(snapshots) - 1; i >= 0; i-- {
			if !jsTruthy(snapshots[i].Get("deletedAt")) {
				base = snapshots[i]
				break
			}
		}
	}
	if !base.IsObject() {
		return nil
	}
	var transcript *protocol.JSONObject
	var err error
	if historyError == nil {
		transcript, err = s.ReconcileArchivedTasks(base, messages)
		if err != nil {
			return err
		}
	}
	terminal, err := terminalLifecycleTasks(base, lifecycle)
	if err != nil {
		return err
	}
	reconciled := preferTerminalTaskFrame(transcript, terminal)
	write := func(next protocol.JSONObject, replace bool, updated string) error {
		all := rows(current.Get("teams"))
		for i, value := range all {
			if value.String("incarnationId") == entry.String("incarnationId") && value.String("teamName") == entry.String("teamName") {
				if replace {
					snapshots[len(snapshots)-1] = next
				} else {
					snapshots = append(snapshots, next)
					if len(snapshots) > 200 {
						snapshots = snapshots[len(snapshots)-200:]
					}
					value.Set("updatedAt", raw(updated))
				}
				value.Set("snapshots", raw(snapshots))
				all[i] = value
				break
			}
		}
		current.Set("teams", raw(all))
		if !replace {
			current.Set("updatedAt", raw(updated))
		}
		return s.writeArchive(path, *current)
	}
	if jsTruthy(latest.Get("deletedAt")) {
		if reconciled == nil || !jsTruthy(reconciled.Get("terminalTaskFrameId")) {
			return nil
		}
		previousRevision, nextRevision := -1.0, -1.0
		if latest.Get("taskListRevision") != nil {
			previousRevision = numeric(latest.Get("taskListRevision"))
		}
		if reconciled.Get("taskListRevision") != nil {
			nextRevision = numeric(reconciled.Get("taskListRevision"))
		}
		if string(latest.Get("terminalTaskFrameId")) == string(reconciled.Get("terminalTaskFrameId")) && previousRevision >= nextRevision {
			return nil
		}
		if latest.Get("taskListRevision") != nil && reconciled.Get("taskListRevision") != nil && nextRevision < previousRevision {
			return nil
		}
		enriched := latest.Clone()
		enriched.Set("version", raw(base.String("version")+":tasks:"+hash(protocol.StringifyValidatedJavaScriptJSON(reconciled.Get("tasks")))+":deleted"))
		enriched.Set("tasks", reconciled.Get("tasks"))
		if reconciled.Get("taskListRevision") != nil {
			enriched.Set("taskListRevision", reconciled.Get("taskListRevision"))
		}
		enriched.Set("terminalTaskFrameId", reconciled.Get("terminalTaskFrameId"))
		return write(enriched, true, "")
	}
	final := latest.Clone()
	if reconciled != nil {
		final.Set("version", raw(latest.String("version")+":tasks:"+hash(protocol.StringifyValidatedJavaScriptJSON(reconciled.Get("tasks")))))
		final.Set("tasks", reconciled.Get("tasks"))
		if reconciled.Get("taskListRevision") != nil {
			final.Set("taskListRevision", reconciled.Get("taskListRevision"))
		}
		if jsTruthy(reconciled.Get("terminalTaskFrameId")) {
			final.Set("terminalTaskFrameId", reconciled.Get("terminalTaskFrameId"))
		}
	}
	deleted := s.options.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	final.Set("version", raw(final.String("version")+":deleted"))
	final.Set("generatedAt", raw(deleted))
	final.Set("deletedAt", raw(deleted))
	team := obj(final.Get("team"))
	team.Set("activeMemberCount", raw(0))
	members := rows(team.Get("members"))
	for i, member := range members {
		member.Set("status", raw("completed"))
		member.Set("activity", raw("exited"))
		members[i] = member
	}
	team.Set("members", raw(members))
	final.Set("team", raw(team))
	return write(final, false, deleted)
}
func (s *Service) GetWorkbenchForSession(ctx context.Context, session string, lookup protocol.JSONObject) (*protocol.JSONObject, error) {
	live, err := s.ListTeams(ctx)
	if err != nil {
		return nil, err
	}
	liveIDs := map[string]bool{}
	for _, summary := range rows(live) {
		team, err := s.GetTeam(ctx, summary.String("name"))
		if err != nil || team.String("leadSessionId") != session {
			continue
		}
		snapshot, err := s.GetWorkbench(ctx, team.String("name"))
		if err == nil {
			liveIDs[obj(snapshot.Get("team")).String("incarnationId")] = true
		}
	}
	archive := s.ReadArchive(session)
	entry := s.SelectArchiveEntry(archive, lookup)
	latest := protocol.JSONObject{}
	if entry != nil {
		latest = lastSnapshot(*entry)
	}
	refresh := latest.IsObject() && jsTruthy(latest.Get("deletedAt")) && !jsTruthy(latest.Get("terminalTaskFrameId"))
	if latest.IsObject() && !jsTruthy(latest.Get("deletedAt")) && !liveIDs[obj(latest.Get("team")).String("incarnationId")] {
		lifecycle, e := s.options.Tasks.ReadLifecycle(teamstore.SanitizeName(obj(latest.Get("team")).String("name")))
		if e == nil {
			terminal, e := terminalLifecycleTasks(latest, lifecycle)
			if e == nil {
				refresh = terminal != nil
			}
		}
	}
	if entry != nil && refresh {
		if err = s.MarkArchiveDeleted(ctx, entry.String("teamName"), session, entry.String("incarnationId")); err != nil {
			return nil, err
		}
		archive = s.ReadArchive(session)
		entry = s.SelectArchiveEntry(archive, lookup)
	}
	timeline := func(entry protocol.JSONObject, source string) *protocol.JSONObject {
		out := protocol.NewJSONObject(field("sessionId", session), protocol.JSONField{Name: "teamName", Value: entry.Get("teamName")}, protocol.JSONField{Name: "incarnationId", Value: entry.Get("incarnationId")}, protocol.JSONField{Name: "snapshots", Value: entry.Get("snapshots")}, field("source", source))
		return &out
	}
	if entry != nil {
		source := "archive"
		if liveIDs[entry.String("incarnationId")] {
			source = "live"
		}
		return timeline(*entry, source), nil
	}
	messages, err := s.readTeamHistory(ctx, session)
	if err != nil {
		if api, ok := err.(*teamsapi.APIError); ok && api.Code == "TEAM_HISTORY_INCOMPLETE" {
			return nil, err
		}
		return nil, nil
	}
	for _, snapshot := range s.ProjectWorkbenches(session, messages) {
		if err = s.ArchiveSnapshot(ctx, snapshot); err != nil {
			return nil, err
		}
	}
	archive = s.ReadArchive(session)
	entry = s.SelectArchiveEntry(archive, lookup)
	if entry == nil {
		return nil, nil
	}
	return timeline(*entry, "transcript"), nil
}
func (s *Service) DeleteTeam(ctx context.Context, name string) error {
	return s.options.Tasks.WithTeamLifecycleTransaction(teamstore.SanitizeName(name), func(scoped *taskstore.Service) error {
		config, err := s.loadConfig(name)
		if err != nil {
			return err
		}
		remaining := 0
		var lead *protocol.JSONObject
		for _, member := range rows(config.Get("members")) {
			if string(member.Get("agentId")) == string(config.Get("leadAgentId")) {
				copy := member
				lead = &copy
			} else {
				remaining++
			}
		}
		if lead == nil || string(lead.Get("isActive")) != "false" || remaining > 0 {
			return &teamsapi.APIError{Status: 409, Code: "CONFLICT", Message: "Cannot delete team \"" + name + "\": lead is active or teammates remain registered"}
		}
		if _, err = s.getWorkbenchLocked(ctx, name, scoped); err != nil {
			return err
		}
		if s.options.CleanupTeam == nil {
			return &teamsapi.CapabilityError{Boundary: "native Team resource cleanup"}
		}
		if err = s.options.CleanupTeam(ctx, config.String("name"), scoped); err != nil {
			return err
		}
		return s.MarkArchiveDeleted(ctx, config.String("name"), config.String("leadSessionId"), incarnation(config))
	})
}
