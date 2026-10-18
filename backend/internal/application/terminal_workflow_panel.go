package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"sort"
	"strings"
)

type terminalWorkflowPanelState struct {
	taskID, level, filter, saveScope string
	phase, agent                     int
}

func workflowNumber(row protocol.JSONObject, key string) int {
	var number float64
	_ = json.Unmarshal(row.Get(key), &number)
	return int(number)
}

type terminalWorkflowPhase struct {
	Index  int                   `json:"index"`
	Title  string                `json:"title"`
	Agents []protocol.JSONObject `json:"agents"`
}

func terminalWorkflowGroups(progress []protocol.JSONObject) ([]terminalWorkflowPhase, []protocol.JSONObject, []string) {
	phases := map[int]*terminalWorkflowPhase{}
	orphans, logs := []protocol.JSONObject{}, []string{}
	for _, row := range progress {
		switch row.String("type") {
		case "workflow_phase":
			index := workflowNumber(row, "index")
			if phases[index] == nil {
				phases[index] = &terminalWorkflowPhase{Index: index, Title: row.String("title"), Agents: []protocol.JSONObject{}}
			}
		case "workflow_log":
			logs = append(logs, row.String("message"))
		default:
			if row.Get("phaseIndex") == nil {
				orphans = append(orphans, row)
				continue
			}
			index := workflowNumber(row, "phaseIndex")
			if phases[index] == nil {
				title := row.String("phaseTitle")
				if row.Get("phaseTitle") == nil || string(row.Get("phaseTitle")) == "null" {
					title = fmt.Sprintf("Phase %d", index)
				}
				phases[index] = &terminalWorkflowPhase{Index: index, Title: title, Agents: []protocol.JSONObject{}}
			}
			phases[index].Agents = append(phases[index].Agents, row)
		}
	}
	ordered := []terminalWorkflowPhase{}
	for _, phase := range phases {
		ordered = append(ordered, *phase)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Index < ordered[j].Index })
	return ordered, orphans, logs
}
func terminalWorkflowFilter(agents []protocol.JSONObject, filter string) []protocol.JSONObject {
	rows := []protocol.JSONObject{}
	for _, row := range agents {
		if filter == "running" && row.String("state") != "start" && row.String("state") != "progress" {
			continue
		}
		if (filter == "done" || filter == "error") && row.String("state") != filter {
			continue
		}
		rows = append(rows, row)
	}
	return rows
}
func (s *Session) terminalWorkflowRows(ctx context.Context) ([]protocol.JSONObject, error) {
	snapshot, err := s.process.TerminalPanelSnapshot(ctx, "tasks")
	if err != nil {
		return nil, err
	}
	var all []protocol.JSONObject
	_ = json.Unmarshal(snapshot.Get("tasks"), &all)
	rows := []protocol.JSONObject{}
	for _, row := range all {
		if row.String("type") == "local_workflow" {
			rows = append(rows, row)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return workflowNumber(rows[i], "startTime") > workflowNumber(rows[j], "startTime")
	})
	return rows, nil
}
func (s *Session) refreshTerminalWorkflowPanel(ctx context.Context, state *terminalLocalPanelState) error {
	rows, err := s.terminalWorkflowRows(ctx)
	if err != nil {
		return err
	}
	p := &state.props
	p.HideDetails = true
	p.Title = "Workflows"
	p.Description = "Dynamic workflow runs in this session. Select a run to inspect its phases and agents."
	p.Actions = []tui.LocalPanelAction{{Key: "r", Name: "refresh", Label: "refresh"}}
	wf := state.workflowPanel
	add := func(id, label, detail string, row protocol.JSONObject, actions ...tui.LocalPanelAction) {
		state.rows[id] = row
		p.Rows = append(p.Rows, tui.LocalPanelRow{ID: id, Label: label, Detail: detail, Actions: actions})
	}
	if wf == nil || wf.taskID == "" {
		for _, row := range rows {
			actions := []tui.LocalPanelAction{{Key: "enter", Name: "workflow-open", Label: "open"}, {Key: "right", Name: "workflow-open", Label: "open"}}
			if row.String("status") == "running" {
				actions = append(actions, tui.LocalPanelAction{Key: "x", Name: "workflow-stop", Label: "stop"})
			}
			add(row.String("id"), row.String("status")+" · "+row.String("description"), row.String("scriptPath"), row, actions...)
		}
		return nil
	}
	task := protocol.NewJSONObject()
	for _, row := range rows {
		if row.String("id") == wf.taskID {
			task = row
			break
		}
	}
	if task.Get("id") == nil {
		state.workflowPanel = nil
		p.Notice = "Workflow task is no longer available"
		return s.refreshTerminalWorkflowPanel(ctx, state)
	}
	p.Title = "Workflow · " + task.String("workflowName")
	p.Description = task.String("status") + " · " + task.String("description") + fmt.Sprintf("\n%d agents · %d tokens · %d tool calls", workflowNumber(task, "agentCount"), workflowNumber(task, "totalTokens"), workflowNumber(task, "totalToolCalls"))
	p.DismissAction = "workflow-back"
	scope := wf.saveScope
	if scope == "" {
		scope = "project"
	}
	p.Actions = []tui.LocalPanelAction{{Key: "tab", Name: "workflow-scope", Label: "save scope: " + scope}, {Key: "s", Name: "workflow-save", Label: "save /" + task.String("workflowName")}, {Key: "space", Name: "close", Label: "close"}, {Key: "left", Name: "workflow-back", Label: "back"}}
	if task.String("status") == "running" {
		p.Actions = append(p.Actions, tui.LocalPanelAction{Key: "p", Name: "workflow-pause", Label: "pause"})
	}
	var progress []protocol.JSONObject
	_ = json.Unmarshal(task.Get("workflowProgress"), &progress)
	phases, orphans, logs := terminalWorkflowGroups(progress)
	if wf.level == "agent" {
		for _, row := range progress {
			if row.String("type") == "workflow_agent" && workflowNumber(row, "index") == wf.agent {
				p.Description += "\n" + row.String("label") + " · " + row.String("state") + "\nPrompt: " + row.String("promptPreview") + "\nModel: " + row.String("model") + "\n" + row.String("resultPreview") + "\n" + row.String("error")
				state.rows["agent"] = row
				p.Rows = []tui.LocalPanelRow{{ID: "agent", Label: row.String("label"), Detail: string(raw(row))}}
				if task.String("status") == "running" {
					p.Rows[0].Actions = []tui.LocalPanelAction{{Key: "x", Name: "workflow-skip", Label: "skip agent"}, {Key: "r", Name: "workflow-retry", Label: "retry agent"}}
				}
				return nil
			}
		}
		wf.level = "phase"
	}
	if task.String("status") == "running" {
		p.Actions = append(p.Actions, tui.LocalPanelAction{Key: "x", Name: "workflow-stop", Label: "stop"})
	}
	if wf.level == "phase" {
		p.Actions = append(p.Actions, tui.LocalPanelAction{Key: "f", Name: "workflow-filter", Label: "filter: " + wf.filter})
		agents := []protocol.JSONObject{}
		found := false
		for _, phase := range phases {
			if phase.Index == wf.phase {
				agents, found = phase.Agents, true
				break
			}
		}
		if !found && wf.phase == 0 {
			agents = orphans
		}
		for _, row := range terminalWorkflowFilter(agents, wf.filter) {
			add(fmt.Sprintf("agent-%d", workflowNumber(row, "index")), row.String("state")+" · "+row.String("label"), row.String("promptPreview"), row, tui.LocalPanelAction{Key: "enter", Name: "workflow-agent", Label: "inspect agent"}, tui.LocalPanelAction{Key: "right", Name: "workflow-agent", Label: "inspect agent"})
		}
		return nil
	}
	for _, phase := range phases {
		row := protocol.NewJSONObject(protocol.JSONField{Name: "index", Value: raw(phase.Index)})
		add(fmt.Sprintf("phase-%d", phase.Index), phase.Title, fmt.Sprintf("%d agents", len(phase.Agents)), row, tui.LocalPanelAction{Key: "enter", Name: "workflow-phase", Label: "open phase"}, tui.LocalPanelAction{Key: "right", Name: "workflow-phase", Label: "open phase"})
	}
	if len(orphans) > 0 {
		row := protocol.NewJSONObject(protocol.JSONField{Name: "index", Value: []byte("0")})
		add("phase-ungrouped", "Ungrouped", "Agents without a phase", row, tui.LocalPanelAction{Key: "enter", Name: "workflow-phase", Label: "open phase"})
	}
	if len(logs) > 6 {
		logs = logs[len(logs)-6:]
	}
	p.Description += "\n" + strings.Join(logs, "\n")
	return nil
}
func (s *Session) applyTerminalWorkflowPanel(ctx context.Context, state *terminalLocalPanelState, result tui.LocalPanelResult, emit tui.Emit) (bool, error) {
	if state.props.Kind != "workflows" {
		return false, errors.New("Invalid workflow panel action")
	}
	if state.workflowPanel == nil {
		state.workflowPanel = &terminalWorkflowPanelState{filter: "all"}
	}
	wf := state.workflowPanel
	row := state.rows[result.Row]
	switch result.Action {
	case "workflow-open":
		if row.String("type") != "local_workflow" {
			return false, errors.New("Workflow run is no longer available")
		}
		wf.taskID, wf.level = result.Row, "run"
	case "workflow-phase":
		wf.phase, wf.level = workflowNumber(row, "index"), "phase"
	case "workflow-agent":
		wf.agent, wf.level = workflowNumber(row, "index"), "agent"
	case "workflow-back":
		switch wf.level {
		case "agent":
			wf.level = "phase"
		case "phase":
			wf.level = "run"
		default:
			state.workflowPanel = nil
		}
	case "workflow-filter":
		filters := []string{"all", "running", "done", "error"}
		for i, filter := range filters {
			if filter == wf.filter {
				wf.filter = filters[(i+1)%len(filters)]
				break
			}
		}
	case "workflow-scope":
		if wf.saveScope == "project" {
			wf.saveScope = "user"
		} else {
			wf.saveScope = "project"
		}
	case "workflow-save":
		scope := wf.saveScope
		if scope == "" {
			scope = "project"
		}
		saved, err := s.process.SaveTerminalWorkflow(ctx, wf.taskID, scope)
		if err != nil {
			return false, err
		}
		if emit != nil {
			if err = emit(tui.Event{Kind: tui.Notice, Text: "Saved /" + saved.String("name") + " to " + saved.String("filePath")}); err != nil {
				return false, err
			}
		}
		return true, nil
	case "workflow-pause":
		paused, err := s.process.TerminalWorkflowControl(ctx, wf.taskID, "pause", "")
		if err != nil {
			return false, err
		}
		if emit != nil {
			if err = emit(tui.Event{Kind: tui.Notice, Text: paused.String("resumePrompt")}); err != nil {
				return false, err
			}
		}
		return true, nil
	case "workflow-stop":
		id := wf.taskID
		if row.String("type") == "local_workflow" {
			id = row.String("id")
		}
		_, err := s.process.StopBackgroundTask(ctx, id)
		if err != nil {
			return false, err
		}
	case "workflow-skip", "workflow-retry":
		action := strings.TrimPrefix(result.Action, "workflow-")
		key := fmt.Sprintf("%s-%d", "", wf.agent)
		rows, err := s.terminalWorkflowRows(ctx)
		if err != nil {
			return false, err
		}
		for _, task := range rows {
			if task.String("id") == wf.taskID {
				key = fmt.Sprintf("%s-%d", task.String("workflowRunId"), wf.agent)
				break
			}
		}
		if _, err = s.process.TerminalWorkflowControl(ctx, wf.taskID, action, key); err != nil {
			return false, err
		}
	default:
		return false, errors.New("Unknown workflow panel action")
	}
	return false, nil
}
