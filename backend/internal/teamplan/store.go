package teamplan

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
)

type Options struct {
	Config             *teamstore.Config
	Tasks              *taskstore.Service
	ConfigDir, WorkDir string
	Now                func() time.Time
	UUID               func() (string, error)
}
type Store struct{ options Options }
type Identity struct {
	PlanID, SessionID, IncarnationID string
	ExpectedRevision                 float64
}

func New(o Options) (*Store, error) {
	if o.Config == nil || o.Tasks == nil || !filepath.IsAbs(o.ConfigDir) || !filepath.IsAbs(o.WorkDir) || o.Now == nil || o.UUID == nil {
		return nil, errors.New("team plan requires original config/task lifecycle/clock/UUID producers")
	}
	return &Store{options: o}, nil
}
func IdentityFrom(v protocol.JSONObject) Identity {
	return Identity{v.String("planId"), v.String("sessionId"), v.String("incarnationId"), numeric(v.Get("expectedRevision"))}
}
func CurrentIdentity(v protocol.JSONObject) Identity {
	return Identity{v.String("planId"), v.String("sessionId"), v.String("incarnationId"), numeric(v.Get("revision"))}
}
func numeric(data json.RawMessage) float64 { var v float64; _ = json.Unmarshal(data, &v); return v }
func (s *Store) Path(team string) string {
	return filepath.Join(s.options.Config.Directory(team), "plan.json")
}
func (s *Store) locked(team string, run func() error) error {
	return s.options.Tasks.WithTeamLifecycleLock(teamstore.SanitizeName(team), run)
}
func (s *Store) Incarnation(team, session string) (string, error) {
	v := s.options.Config.ReadAsync(team)
	if v == nil || v.String("leadSessionId") != session {
		return "", failure("Team does not belong to this session", 404)
	}
	lead := v.Get("leadSessionId")
	if len(lead) == 0 || string(lead) == "null" {
		lead = raw("")
	}
	data := protocol.StringifyValidatedJavaScriptJSON(raw([]json.RawMessage{v.Get("name"), lead, v.Get("createdAt")}))
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
func (s *Store) Read(team string) (*protocol.JSONObject, error) {
	data, e := os.ReadFile(s.Path(team))
	if errors.Is(e, os.ErrNotExist) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	v, e := ParseRecord(data)
	if e != nil {
		return nil, e
	}
	return &v, nil
}
func (s *Store) write(plan protocol.JSONObject) error {
	v, e := ParseRecord(raw(plan))
	if e != nil {
		return e
	}
	team := plan.String("teamName")
	if e = os.MkdirAll(s.options.Config.Directory(team), 0777); e != nil {
		return e
	}
	id, e := s.options.UUID()
	if e != nil {
		return e
	}
	path := s.Path(team)
	temp := path + "." + id + ".tmp"
	defer os.Remove(temp)
	data, e := v.MarshalJSON()
	if e != nil {
		return e
	}
	var pretty json.RawMessage
	pretty = data
	var b bytes.Buffer
	if e = json.Indent(&b, protocol.StringifyValidatedJavaScriptJSON(pretty), "", "  "); e != nil {
		return e
	}
	if e = os.WriteFile(temp, b.Bytes(), 0666); e != nil {
		return e
	}
	return os.Rename(temp, path)
}
func (s *Store) EnsureDraft(team, session string, runtime, initial protocol.JSONObject) (out protocol.JSONObject, err error) {
	err = s.locked(team, func() error {
		incarnation, e := s.Incarnation(team, session)
		if e != nil {
			return e
		}
		existing, e := s.Read(team)
		if e != nil {
			return e
		}
		if existing != nil && existing.String("incarnationId") == incarnation {
			out = existing.Clone()
			return nil
		}
		id, e := s.options.UUID()
		if e != nil {
			return e
		}
		now := s.options.Now().UnixMilli()
		workDir := initial.Get("workDir")
		if workDir == nil || string(workDir) == "null" {
			workDir = raw(s.options.WorkDir)
		}
		members, tasks := initial.Get("members"), initial.Get("tasks")
		if members == nil || string(members) == "null" {
			members = []byte(`[]`)
		}
		if tasks == nil || string(tasks) == "null" {
			tasks = []byte(`[]`)
		}
		out, e = ParseRecord(raw(protocol.NewJSONObject(protocol.JSONField{Name: "schemaVersion", Value: raw(1)}, protocol.JSONField{Name: "planId", Value: raw(id)}, protocol.JSONField{Name: "sessionId", Value: raw(session)}, protocol.JSONField{Name: "teamName", Value: raw(team)}, protocol.JSONField{Name: "incarnationId", Value: raw(incarnation)}, protocol.JSONField{Name: "revision", Value: raw(1)}, protocol.JSONField{Name: "state", Value: raw("draft")}, protocol.JSONField{Name: "workDir", Value: workDir}, protocol.JSONField{Name: "leaderRuntime", Value: raw(runtime)}, protocol.JSONField{Name: "agentCatalog", Value: initial.Get("agentCatalog")}, protocol.JSONField{Name: "members", Value: members}, protocol.JSONField{Name: "tasks", Value: tasks}, protocol.JSONField{Name: "createdAt", Value: raw(now)}, protocol.JSONField{Name: "updatedAt", Value: raw(now)})))
		if e != nil {
			return e
		}
		if e = ValidateGraph(out, false); e != nil {
			return e
		}
		return s.write(out)
	})
	return
}
func (s *Store) requireCurrent(team string, id Identity) (*protocol.JSONObject, error) {
	v, e := s.Read(team)
	if e != nil {
		return nil, e
	}
	if v == nil || v.String("planId") != id.PlanID || v.String("sessionId") != id.SessionID || v.String("incarnationId") != id.IncarnationID {
		return nil, failure("Plan identity changed; refresh before continuing", 409)
	}
	incarnation, e := s.Incarnation(team, id.SessionID)
	if e != nil {
		return nil, e
	}
	if incarnation != id.IncarnationID {
		return nil, failure("Plan identity changed; refresh before continuing", 409)
	}
	return v, nil
}
func (s *Store) Mutate(team string, id Identity, fn func(protocol.JSONObject) (protocol.JSONObject, error)) (out protocol.JSONObject, err error) {
	err = s.locked(team, func() error {
		v, e := s.requireCurrent(team, id)
		if e != nil {
			return e
		}
		if numeric(v.Get("revision")) != id.ExpectedRevision {
			return failure("Plan changed; refresh before continuing", 409)
		}
		out, e = fn(v.Clone())
		if e != nil {
			return e
		}
		out.Set("revision", raw(numeric(v.Get("revision"))+1))
		out.Set("updatedAt", raw(s.options.Now().UnixMilli()))
		if e = ValidateGraph(out, false); e != nil {
			return e
		}
		return s.write(out)
	})
	return
}
func preserveRuntime(previous, next protocol.JSONObject) protocol.JSONObject {
	out := previous.Clone()
	if !out.IsObject() {
		return next.Clone()
	}
	for _, key := range []string{"providerId", "modelId", "effortLevel"} {
		out.Delete(key)
	}
	out.Assign(next)
	return out
}
func ApplyDraftPatch(plan, patch protocol.JSONObject, preserveHuman bool) protocol.JSONObject {
	next := plan.Clone()
	next.Assign(patch)
	if patch.Get("members") != nil {
		var members, previous []protocol.JSONObject
		_ = json.Unmarshal(patch.Get("members"), &members)
		_ = json.Unmarshal(plan.Get("members"), &previous)
		for i, member := range members {
			old := protocol.NewJSONObject()
			for _, p := range previous {
				if p.String("id") == member.String("id") {
					old = p
					break
				}
			}
			merged := old.Clone()
			merged.Assign(member)
			merged.Set("runtime", raw(preserveRuntime(object(old.Get("runtime")), object(member.Get("runtime")))))
			if preserveHuman && old.String("runtimeSource") == "human" {
				merged.Set("runtime", old.Get("runtime"))
				merged.Set("runtimeSource", raw("human"))
				suggested := member.Get("suggestedRuntime")
				if suggested == nil || string(suggested) == "null" {
					suggested = member.Get("runtime")
				}
				merged.Set("suggestedRuntime", suggested)
			}
			members[i] = merged
		}
		next.Set("members", raw(members))
	}
	if patch.Get("tasks") != nil {
		var tasks, previous []protocol.JSONObject
		_ = json.Unmarshal(patch.Get("tasks"), &tasks)
		_ = json.Unmarshal(plan.Get("tasks"), &previous)
		for i, task := range tasks {
			merged := protocol.NewJSONObject()
			for _, p := range previous {
				if p.String("id") == task.String("id") {
					merged = p.Clone()
					break
				}
			}
			merged.Assign(task)
			tasks[i] = merged
		}
		next.Set("tasks", raw(tasks))
	}
	return next
}
func (s *Store) Replace(team string, id Identity, patch protocol.JSONObject, preserveReview bool) (protocol.JSONObject, error) {
	return s.Mutate(team, id, func(v protocol.JSONObject) (protocol.JSONObject, error) {
		state := v.String("state")
		if state != "draft" && state != "review_pending" {
			return v, failure("Only a draft or pending review can be edited", 409)
		}
		v = ApplyDraftPatch(v, patch, !preserveReview)
		if !preserveReview {
			v.Set("state", raw("draft"))
		}
		return v, nil
	})
}
func (s *Store) Submit(team string, id Identity, patch *protocol.JSONObject) (protocol.JSONObject, error) {
	return s.Mutate(team, id, func(v protocol.JSONObject) (protocol.JSONObject, error) {
		if state := v.String("state"); state != "draft" && state != "review_pending" {
			return v, failure("Plan cannot be submitted", 409)
		}
		if patch != nil {
			v = ApplyDraftPatch(v, *patch, true)
		}
		if e := ValidateGraph(v, true); e != nil {
			return v, e
		}
		v.Set("state", raw("review_pending"))
		return v, nil
	})
}
func (s *Store) Approve(team string, id Identity, requestID string, validated protocol.JSONObject) (out protocol.JSONObject, committed bool, err error) {
	if protocol.TrimJavaScriptString(requestID) == "" {
		return out, false, failure("requestId is required", 400)
	}
	err = s.locked(team, func() error {
		v, e := s.requireCurrent(team, id)
		if e != nil {
			return e
		}
		if object(v.Get("approvedSnapshot")).String("requestId") == requestID {
			out = v.Clone()
			return nil
		}
		if numeric(v.Get("revision")) != id.ExpectedRevision || numeric(validated.Get("revision")) != numeric(v.Get("revision")) {
			return failure("Plan changed; review it again", 409)
		}
		if v.String("state") != "review_pending" {
			return failure("Plan is not awaiting review", 409)
		}
		if e = ValidateGraph(validated, true); e != nil {
			return e
		}
		out = v.Clone()
		now := s.options.Now().UnixMilli()
		out.Set("members", validated.Get("members"))
		out.Set("state", raw("launching"))
		out.Set("revision", raw(numeric(v.Get("revision"))+1))
		out.Set("updatedAt", raw(now))
		out.Set("approvedSnapshot", raw(protocol.NewJSONObject(protocol.JSONField{Name: "revision", Value: v.Get("revision")}, protocol.JSONField{Name: "members", Value: validated.Get("members")}, protocol.JSONField{Name: "tasks", Value: v.Get("tasks")}, protocol.JSONField{Name: "leaderRuntime", Value: v.Get("leaderRuntime")}, protocol.JSONField{Name: "approvedAt", Value: raw(now)}, protocol.JSONField{Name: "requestId", Value: raw(requestID)})))
		launch := object(out.Get("launch"))
		if !launch.IsObject() {
			launch = protocol.NewJSONObject()
		}
		launch.Set("status", raw("pending"))
		out.Set("launch", raw(launch))
		if e = s.write(out); e != nil {
			return e
		}
		committed = true
		return nil
	})
	return
}
func (s *Store) IsApproved(team, member string) (bool, error) {
	v, e := s.Read(team)
	if e != nil || v == nil {
		return false, e
	}
	inc, e := s.Incarnation(team, v.String("sessionId"))
	if e != nil {
		return false, e
	}
	if inc != v.String("incarnationId") {
		return false, nil
	}
	contains := func(plan protocol.JSONObject) bool {
		snapshot := object(plan.Get("approvedSnapshot"))
		if !snapshot.IsObject() {
			return false
		}
		if member == "" {
			return true
		}
		var members []protocol.JSONObject
		_ = json.Unmarshal(snapshot.Get("members"), &members)
		for _, m := range members {
			if m.String("name") == member || m.String("id") == member {
				return true
			}
		}
		return false
	}
	if (v.String("state") == "launching" || v.String("state") == "running") && contains(*v) {
		return true, nil
	}
	parent := ""
	if member != "" {
		parent = v.String("parentPlanId")
	}
	visited := map[string]bool{}
	for parent != "" && !visited[parent] {
		visited[parent] = true
		if !regexp.MustCompile(`(?i)^[a-f0-9-]{36}$`).MatchString(parent) {
			return false, nil
		}
		data, e := os.ReadFile(filepath.Join(s.options.Config.Directory(team), "plan-"+parent+".json"))
		if e != nil {
			return false, nil
		}
		archive, e := ParseRecord(data)
		if e != nil || archive.String("incarnationId") != v.String("incarnationId") {
			return false, nil
		}
		if archive.String("state") == "running" && contains(archive) {
			return true, nil
		}
		parent = archive.String("parentPlanId")
	}
	return false, nil
}
func (s *Store) FindForSession(session string) (*protocol.JSONObject, error) {
	entries, e := os.ReadDir(filepath.Join(s.options.ConfigDir, "teams"))
	if errors.Is(e, os.ErrNotExist) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	plans := []protocol.JSONObject{}
	for _, entry := range entries {
		v, e := s.Read(entry.Name())
		if e != nil || v == nil || v.String("sessionId") != session {
			continue
		}
		inc, e := s.Incarnation(entry.Name(), session)
		if e == nil && inc == v.String("incarnationId") {
			plans = append(plans, *v)
		}
	}
	sort.SliceStable(plans, func(i, j int) bool { return numeric(plans[i].Get("updatedAt")) > numeric(plans[j].Get("updatedAt")) })
	if len(plans) == 0 {
		return nil, nil
	}
	return &plans[0], nil
}

func (s *Store) StageMember(team string, member protocol.JSONObject) (out protocol.JSONObject, err error) {
	err = s.locked(team, func() error {
		plan, e := s.Read(team)
		if e != nil {
			return e
		}
		if plan == nil {
			return failure("Create the team draft first", 409)
		}
		if _, e = s.requireCurrent(team, CurrentIdentity(*plan)); e != nil {
			return e
		}
		approved, e := s.IsApproved(team, member.String("name"))
		if e != nil {
			return e
		}
		if approved {
			return failure("A running team already has member "+member.String("name")+". Give it more work with SendMessage instead; a stopped member restarts from its saved conversation when messaged.", 409)
		}
		out = plan.Clone()
		if out.String("state") == "running" {
			var b bytes.Buffer
			if e = json.Indent(&b, protocol.StringifyValidatedJavaScriptJSON(raw(out)), "", "  "); e != nil {
				return e
			}
			if e = os.WriteFile(filepath.Join(s.options.Config.Directory(team), "plan-"+out.String("planId")+".json"), b.Bytes(), 0666); e != nil {
				return e
			}
			id, e := s.options.UUID()
			if e != nil {
				return e
			}
			out.Set("parentPlanId", out.Get("planId"))
			out.Set("planId", raw(id))
			out.Set("revision", raw(0))
			out.Set("state", raw("draft"))
			out.Set("members", []byte(`[]`))
			out.Set("tasks", []byte(`[]`))
			out.Delete("approvedSnapshot")
			out.Delete("launch")
		}
		if state := out.String("state"); state != "draft" && state != "review_pending" {
			return failure("Plan is not editable", 409)
		}
		var members []protocol.JSONObject
		_ = json.Unmarshal(out.Get("members"), &members)
		previousIndex := -1
		previous := protocol.NewJSONObject()
		for i, old := range members {
			if old.String("id") == member.String("id") || old.String("name") == member.String("name") {
				previousIndex, previous = i, old
				break
			}
		}
		value := previous.Clone()
		value.Assign(member)
		value.Set("runtime", raw(preserveRuntime(object(previous.Get("runtime")), object(member.Get("runtime")))))
		if previous.Get("id") != nil {
			value.Set("id", previous.Get("id"))
		}
		if previous.String("runtimeSource") == "human" {
			value.Set("runtime", previous.Get("runtime"))
			value.Set("runtimeSource", raw("human"))
			suggested := member.Get("suggestedRuntime")
			if suggested == nil || string(suggested) == "null" {
				suggested = member.Get("runtime")
			}
			value.Set("suggestedRuntime", suggested)
		}
		nextMembers := []protocol.JSONObject{}
		for i, old := range members {
			if i != previousIndex {
				nextMembers = append(nextMembers, old)
			}
		}
		nextMembers = append(nextMembers, value)
		out.Set("members", raw(nextMembers))
		out.Set("state", raw("draft"))
		out.Set("revision", raw(numeric(out.Get("revision"))+1))
		out.Set("updatedAt", raw(s.options.Now().UnixMilli()))
		if e = ValidateGraph(out, false); e != nil {
			return e
		}
		return s.write(out)
	})
	return
}
