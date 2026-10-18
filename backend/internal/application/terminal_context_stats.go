package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/contextusage"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"sort"
	"strconv"
	"time"
)

func terminalContextStatsCommands() []tui.Completion {
	return []tui.Completion{{Name: "context", Description: "Visualize current context usage as a colored grid", Kind: "local-jsx"}, {Name: "effort", Description: "Set effort level for model usage", ArgumentHint: "[low|medium|high|max|auto]", Kind: "local-jsx"}, {Name: "stats", Description: "Show your Claude Code usage statistics and activity", Kind: "local-jsx"}}
}
func (s *Session) invokeTerminalContextStatsCommand(ctx context.Context, name, args string, emit tui.Emit) (bool, error) {
	if name != "context" && name != "effort" && name != "stats" {
		return false, nil
	}
	if !s.terminalInteractive {
		return false, nil
	}
	switch name {
	case "context":
		value, err := s.process.ReadContextUsage(ctx, false)
		if err != nil {
			return true, err
		}
		var data contextusage.Data
		encoded, err := value.MarshalJSON()
		if err != nil {
			return true, err
		}
		if err = json.Unmarshal(encoded, &data); err != nil {
			return true, err
		}
		if emit != nil {
			return true, emit(tui.Event{Kind: tui.EventKind("context_usage_generated"), Result: encoded, Background: true})
		}
		return true, nil
	case "effort":
		output, err := s.process.TerminalEffort(ctx, args)
		if err != nil {
			return true, err
		}
		if emit != nil {
			return true, emit(tui.Event{Kind: tui.LocalCommandOutput, Text: output, Background: true})
		}
		return true, nil
	}
	all, err := s.process.TerminalActivityStats(ctx, "all")
	if err != nil {
		return true, err
	}
	state := &terminalLocalPanelState{props: tui.LocalPanel{ID: newID(), Kind: "stats"}, rows: map[string]protocol.JSONObject{"_all": all, "_current": all}, form: "all", selected: "Overview"}
	if err = s.refreshTerminalContextStatsPanel(ctx, state); err != nil {
		return true, err
	}
	return true, s.openTerminalConversationPanel(state, emit)
}

func statsNumber(o protocol.JSONObject, key string) float64 {
	var n float64
	_ = json.Unmarshal(o.Get(key), &n)
	return n
}
func statsInt(o protocol.JSONObject, key string) string {
	return strconv.FormatFloat(statsNumber(o, key), 'f', -1, 64)
}

type terminalModelStat struct {
	Name  string
	Usage protocol.JSONObject
	Total float64
}

func terminalStatsModels(stats protocol.JSONObject) ([]terminalModelStat, float64) {
	var entries map[string]protocol.JSONObject
	_ = json.Unmarshal(stats.Get("modelUsage"), &entries)
	names := []string{}
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	models := []terminalModelStat{}
	total := float64(0)
	for _, name := range names {
		usage := entries[name]
		tokens := statsNumber(usage, "inputTokens") + statsNumber(usage, "outputTokens")
		total += tokens
		models = append(models, terminalModelStat{name, usage, tokens})
	}
	sort.SliceStable(models, func(i, j int) bool { return models[i].Total > models[j].Total })
	return models, total
}
func (s *Session) refreshTerminalContextStatsPanel(ctx context.Context, state *terminalLocalPanelState) error {
	if state.props.Kind != "stats" {
		return nil
	}
	current, all := state.rows["_current"], state.rows["_all"]
	p := &state.props
	p.Title = "Claude Code usage statistics · " + state.selected
	label := map[string]string{"all": "All time", "7d": "Last 7 days", "30d": "Last 30 days"}[state.form]
	p.Description = label
	if value, err := s.process.TerminalConversation(ctx); err == nil {
		preferences, err := s.TerminalPreferences(ctx)
		if err != nil {
			return err
		}
		labels, err := s.process.TerminalStatsModelLabels(ctx, current)
		if err != nil {
			return err
		}
		clock := value.Now
		if zone := s.native.Environment["TZ"]; zone != "" {
			if location, err := time.LoadLocation(zone); err == nil {
				clock = clock.In(location)
			}
		}
		p.Presentation = raw(tui.StatsPresentation{Stats: current, AllTime: all, Range: state.form, Tab: state.selected, Now: clock, Theme: preferences.Theme, Labels: labels})
	} else {
		return err
	}
	p.Rows = nil
	p.HideDetails = false
	p.RawInputKeys = []string{"r", "tab", "ctrl+s", "ctrl+d"}
	p.Actions = []tui.LocalPanelAction{{Key: "tab", Name: "stats-tab", Label: "Overview / Models"}, {Key: "r", Name: "stats-range", Label: "cycle dates"}, {Key: "ctrl+s", Name: "stats-copy", Label: "copy screenshot"}, {Key: "ctrl+d", Name: "stats-close", Label: "close"}}
	add := func(id, name, value string) {
		p.Rows = append(p.Rows, tui.LocalPanelRow{ID: id, Label: name, Detail: value})
	}
	if statsNumber(all, "totalSessions") == 0 {
		add("empty", "No Claude Code usage data available", "")
		return nil
	}
	models, total := terminalStatsModels(current)
	if state.selected == "Models" {
		if len(models) == 0 {
			add("empty", "No model usage data available", "")
			return nil
		}
		for _, model := range models {
			percentage := float64(0)
			if total != 0 {
				percentage = model.Total / total * 100
			}
			add("model:"+model.Name, model.Name, fmt.Sprintf("%.1f%% · In: %s · Out: %s", percentage, statsInt(model.Usage, "inputTokens"), statsInt(model.Usage, "outputTokens")))
		}
		var daily []protocol.JSONObject
		_ = json.Unmarshal(current.Get("dailyModelTokens"), &daily)
		for _, day := range daily {
			var tokens map[string]float64
			_ = json.Unmarshal(day.Get("tokensByModel"), &tokens)
			sum := float64(0)
			for _, n := range tokens {
				sum += n
			}
			add("tokens:"+day.String("date"), "Tokens per Day · "+day.String("date"), strconv.FormatFloat(sum, 'f', -1, 64))
		}
		return nil
	}
	if len(models) > 0 {
		add("favorite", "Favorite model", models[0].Name)
	}
	add("tokens", "Total tokens", strconv.FormatFloat(total, 'f', -1, 64))
	add("sessions", "Sessions", statsInt(current, "totalSessions"))
	if longest, ok := current.Object("longestSession"); ok {
		add("longest", "Longest session", time.Duration(statsNumber(longest, "duration")*float64(time.Millisecond)).String())
	}
	days := statsInt(current, "totalDays")
	if state.form == "7d" {
		days = "7"
	}
	if state.form == "30d" {
		days = "30"
	}
	add("active", "Active days", statsInt(current, "activeDays")+"/"+days)
	streaks, _ := current.Object("streaks")
	allStreaks, _ := all.Object("streaks")
	add("longest-streak", "Longest streak", statsInt(streaks, "longestStreak")+" days")
	add("current-streak", "Current streak", statsInt(allStreaks, "currentStreak")+" days")
	if peak := current.String("peakActivityDay"); peak != "" {
		add("peak", "Most active day", peak)
	}
	// The all-time activity grid stays all-time when the range selector changes.
	var daily []protocol.JSONObject
	_ = json.Unmarshal(all.Get("dailyActivity"), &daily)
	for _, day := range daily {
		add("activity:"+day.String("date"), "Activity · "+day.String("date"), statsInt(day, "messageCount")+" messages · "+statsInt(day, "sessionCount")+" sessions")
	}
	return nil
}

func (s *Session) applyTerminalContextStatsPanel(ctx context.Context, result tui.LocalPanelResult, emit tui.Emit) (bool, error) {
	s.mu.Lock()
	state := s.terminalPanel
	s.mu.Unlock()
	if state == nil || state.props.ID != result.ID || state.props.Kind != "stats" {
		return false, nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	s.mu.Lock()
	active := s.terminalPanel == state
	s.mu.Unlock()
	if !active {
		return true, errors.New("Local panel is no longer active")
	}
	switch result.Action {
	case "close", "stats-close":
		if err := s.closeTerminalConversationPanel(state.props.ID, emit); err != nil {
			return true, err
		}
		return true, s.terminalConversationNotice(emit, "Stats dialog dismissed")
	case "stats-tab":
		if state.selected == "Overview" {
			state.selected = "Models"
		} else {
			state.selected = "Overview"
		}
	case "stats-range":
		next := map[string]string{"all": "7d", "7d": "30d", "30d": "all"}[state.form]
		data, exists := state.rows["_range:"+next]
		if next == "all" {
			data, exists = state.rows["_all"], true
		}
		if !exists {
			var err error
			data, err = s.process.TerminalActivityStats(ctx, next)
			if err != nil {
				state.props.Notice = err.Error()
				return true, s.openTerminalConversationPanel(state, emit)
			}
			state.rows["_range:"+next] = data
		}
		state.form, state.rows["_current"] = next, data
	case "stats-copy":
		var presentation tui.StatsPresentation
		if err := json.Unmarshal(state.props.Presentation, &presentation); err != nil {
			return true, err
		}
		err := s.process.CopyTerminalStatsScreenshot(ctx, tui.StatsScreenshotANSI(presentation))
		state.props.Notice = "copied!"
		state.props.NoticeUntil = time.Now().Add(2 * time.Second)
		if err != nil {
			state.props.Notice = "copy failed"
		}
		return true, s.openTerminalConversationPanel(state, emit)
	default:
		return true, fmt.Errorf("Invalid stats action: %s", result.Action)
	}
	if err := s.refreshTerminalContextStatsPanel(ctx, state); err != nil {
		return true, err
	}
	return true, s.openTerminalConversationPanel(state, emit)
}
