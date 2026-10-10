package teamreader

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
	"math"
	"regexp"
	"sort"
	"strconv"
)

var taskNumberPrefix = regexp.MustCompile(`^[+-]?[0-9]+`)

func taskNumber(value string) float64 {
	prefix := taskNumberPrefix.FindString(protocol.TrimJavaScriptString(value))
	if prefix == "" {
		return math.NaN()
	}
	n, err := strconv.ParseFloat(prefix, 64)
	if err != nil && !math.IsInf(n, 0) {
		return math.NaN()
	}
	return n
}
func sortTasks(tasks []protocol.JSONObject) {
	sort.SliceStable(tasks, func(i, j int) bool {
		left, right := taskNumber(tasks[i].String("id")), taskNumber(tasks[j].String("id"))
		if !math.IsNaN(left) && !math.IsInf(left, 0) && !math.IsNaN(right) && !math.IsInf(right, 0) {
			return left < right
		}
		return localeCompare(tasks[i].String("id"), tasks[j].String("id")) < 0
	})
}
func jsTruthy(value []byte) bool {
	switch string(value) {
	case "", "null", "false", "0", "-0", `""`:
		return false
	}
	return true
}
func (s *Service) GetWorkbench(ctx context.Context, name string) (out protocol.JSONObject, err error) {
	if err = context.Cause(ctx); err != nil {
		return out, err
	}
	err = s.options.Tasks.WithTeamLifecycleTransaction(teamstore.SanitizeName(name), func(scoped *taskstore.Service) error {
		var failure error
		out, failure = s.getWorkbenchLocked(ctx, name, scoped)
		return failure
	})
	return
}
func (s *Service) getWorkbenchLocked(ctx context.Context, name string, scoped *taskstore.Service) (protocol.JSONObject, error) {
	config, err := s.loadConfig(name)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	list := teamstore.SanitizeName(config.String("name"))
	team, err := s.GetTeam(ctx, name)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	roster := map[string]bool{}
	for _, member := range rows(config.Get("members")) {
		roster[member.String("agentId")] = true
	}
	members := []protocol.JSONObject{}
	active := 0
	for _, member := range rows(team.Get("members")) {
		if roster[member.String("agentId")] {
			members = append(members, member)
			if member.String("status") == "running" {
				active++
			}
		}
	}
	team.Set("memberCount", raw(len(members)))
	team.Set("activeMemberCount", raw(active))
	team.Set("members", raw(members))
	frame, err := scoped.Snapshot(list)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	tasks := []protocol.JSONObject{}
	for _, task := range frame.Tasks {
		if jsTruthy(obj(task.Get("metadata")).Get("_internal")) {
			continue
		}
		copy := task.Clone()
		copy.Set("taskListId", raw(list))
		tasks = append(tasks, copy)
	}
	sortTasks(tasks)
	messages, err := s.ReadWorkbenchMessages(name)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	canonical := protocol.NewJSONObject(field("team", team), field("tasks", tasks), field("messages", messages))
	snapshot := protocol.NewJSONObject(field("version", hash(protocol.StringifyValidatedJavaScriptJSON(raw(canonical)))), field("generatedAt", frame.CapturedAt), field("taskListRevision", frame.Revision), field("team", team), field("tasks", tasks), field("messages", messages))
	if err = s.ArchiveSnapshot(ctx, snapshot); err != nil {
		return protocol.JSONObject{}, err
	}
	return snapshot, nil
}
