package clibootstrap

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/preflightcore"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf16"
)

func (m *MainPrint) nativeCompactAttachments(ctx context.Context, scope *cooperative.Scope, keep []protocol.JSONObject) ([]protocol.JSONObject, error) {
	if !scope.BelongsTo(m.Owner) {
		return nil, cooperative.ErrRestricted
	}
	out := []protocol.JSONObject{}
	create := func(payload protocol.JSONObject) { out = append(out, m.factory.CreateAttachment(rawMain(payload))) }
	tasks, e := m.nativeBackgroundBindings().List(scope)
	if e != nil {
		return nil, e
	}
	directory, e := m.nativeTaskOutputDirectory(scope)
	if e != nil {
		return nil, e
	}
	for _, task := range tasks {
		if task.String("type") != "local_agent" || task.String("status") == "pending" || string(task.Get("retrieved")) == "true" || task.String("agentId") == m.options.AgentID {
			continue
		}
		delta := task.Get("error")
		if task.String("status") == "running" {
			progress, _ := task.Object("progress")
			delta = progress.Get("summary")
		}
		if delta == nil {
			delta = rawMain(nil)
		}
		id := task.String("agentId")
		if id == "" {
			id = task.String("id")
		}
		create(protocol.NewJSONObject(fieldMain("type", "task_status"), fieldMain("taskId", id), fieldMain("taskType", "local_agent"), protocol.JSONField{Name: "description", Value: task.Get("description")}, protocol.JSONField{Name: "status", Value: task.Get("status")}, protocol.JSONField{Name: "deltaSummary", Value: delta}, fieldMain("outputFilePath", filepath.Join(directory, id+".output"))))
	}
	if m.plans != nil {
		content, path, e := m.plans.Read(scope, m.options.AgentID)
		if e != nil {
			return nil, e
		}
		if string(content) != "null" && string(content) != "\"\"" {
			create(protocol.NewJSONObject(fieldMain("type", "plan_file_reference"), fieldMain("planFilePath", path), protocol.JSONField{Name: "planContent", Value: content}))
		}
	}
	preserved := make([]queryloop.Message, len(keep))
	for i, record := range keep {
		preserved[i] = queryloop.Message{Record: record}
	}
	plans, e := m.nativePlanAttachments(scope, preserved)
	if e != nil {
		return nil, e
	}
	for _, record := range plans {
		out = append(out, record.Record)
	}
	skills, e := m.Controller.CompactInvokedSkillsOnOwner(scope, m.options.AgentID)
	if e != nil {
		return nil, e
	}
	sort.SliceStable(skills, func(i, j int) bool {
		var a, b float64
		_ = json.Unmarshal(skills[i].Get("invokedAt"), &a)
		_ = json.Unmarshal(skills[j].Get("invokedAt"), &b)
		return a > b
	})
	selected := []protocol.JSONObject{}
	used := float64(0)
	for _, skill := range skills {
		units, e := protocol.DecodeJavaScriptJSONString(skill.Get("content"))
		if e != nil {
			return nil, e
		}
		const marker = "\n\n[... skill content truncated for compaction; use Read on the skill path if you need the full text]"
		if len(units) > 20000 {
			units = append(units[:20000-len(marker)], utf16.Encode([]rune(marker))...)
		}
		content := protocol.QuoteJavaScriptUTF16(units)
		tokens, e := preflightcore.RoughContent(content)
		if e != nil {
			return nil, e
		}
		if used+tokens > 25000 {
			continue
		}
		used += tokens
		selected = append(selected, protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: skill.Get("skillName")}, protocol.JSONField{Name: "path", Value: skill.Get("skillPath")}, protocol.JSONField{Name: "content", Value: content}))
	}
	if len(selected) > 0 {
		create(protocol.NewJSONObject(fieldMain("type", "invoked_skills"), fieldMain("skills", selected)))
	}
	delta, e := m.nativeDeferredToolsAttachments(scope, preserved)
	if e != nil {
		return nil, e
	}
	for _, record := range delta {
		out = append(out, record.Record)
	}
	// Keep sent-skill identity intact: compaction restores invoked skills without
	// resetting the ordinary skill-listing gate or reloading all skill bodies.
	m.dynamicSkillDirTriggers = nil
	return out, ctx.Err()
}

func (m *MainPrint) nativeCompactExcludedFiles(scope *cooperative.Scope) ([]string, error) {
	if !scope.BelongsTo(m.Owner) {
		return nil, cooperative.ErrRestricted
	}
	n := m.options.Native
	config := n.ConfigHomeDirectory()
	if config == "" {
		config = filepath.Join(n.Home, ".claude")
	}
	out := []string{filepath.Join(config, "CLAUDE.md"), filepath.Join(n.CWD, "CLAUDE.md"), filepath.Join(n.CWD, "CLAUDE.local.md"), filepath.Join(n.ManagedRoot, "CLAUDE.md")}
	if m.plans != nil {
		_, path, e := m.plans.Read(scope, m.options.AgentID)
		if e != nil {
			return nil, e
		}
		out = append(out, path)
	}
	if m.memoryPaths != nil {
		directory, e := m.memoryPaths.Directory()
		if e != nil {
			return nil, e
		}
		out = append(out, filepath.Join(directory, "MEMORY.md"))
	}
	return out, nil
}
func (m *MainPrint) nativeCompactCleanup(scope *cooperative.Scope) error {
	if !scope.BelongsTo(m.Owner) {
		return cooperative.ErrRestricted
	}
	source := m.options.QuerySource
	main := source == "" || source == "sdk" || strings.HasPrefix(source, "repl_main_thread")
	if m.userContext != nil {
		m.userContext.ResetAfterCompact(main)
	}
	if m.prompt != nil {
		m.prompt.Clear()
	}
	return nil
}
