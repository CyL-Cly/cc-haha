package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
)

// Catalog and Selection are process-local capabilities made exclusively from
// the actual native log enumeration. Detached menu rows cannot supply a log.
type TerminalResumeCatalog struct {
	main               *MainPrint
	picker             *CLIResumePicker
	logs               []protocol.JSONObject
	currentSID, branch string
	all                bool
}
type TerminalResumeSelection struct {
	catalog *TerminalResumeCatalog
	log     protocol.JSONObject
	sid     string
}

// Presentation exposes only a detached display copy. Confirmation keeps the
// actual captured selection capability rather than accepting these JSON rows.
func (s *TerminalResumeSelection) Presentation() protocol.JSONObject {
	if s == nil {
		return protocol.JSONObject{}
	}
	return s.log.Clone()
}

func (s *TerminalResumeSelection) HasFullMessages() bool {
	if s == nil {
		return false
	}
	var messages []json.RawMessage
	return json.Unmarshal(s.log.Get("messages"), &messages) == nil && len(messages) > 0
}

func (c *TerminalResumeCatalog) Presentation() protocol.JSONObject {
	rows := make([]protocol.JSONObject, len(c.logs))
	for i, v := range c.logs {
		rows[i] = v.Clone()
	}
	return protocol.NewJSONObject(protocol.JSONField{Name: "logs", Value: rawMain(rows)}, protocol.JSONField{Name: "currentDirectory", Value: quoteMain(c.picker.CurrentDirectory())}, protocol.JSONField{Name: "currentBranch", Value: quoteMain(c.branch)}, protocol.JSONField{Name: "hasMultipleWorktrees", Value: rawMain(len(c.picker.worktrees) > 1)}, protocol.JSONField{Name: "allProjects", Value: rawMain(c.all)})
}
func terminalLogSID(log protocol.JSONObject) string {
	if sid := log.String("sessionId"); sid != "" {
		return sid
	}
	var messages []protocol.JSONObject
	_ = json.Unmarshal(log.Get("messages"), &messages)
	if len(messages) > 0 {
		return messages[0].String("sessionId")
	}
	return ""
}
func (m *MainPrint) terminalResumeRead(ctx context.Context, fn func(*cooperative.Scope) error) error {
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if m.closed || m.shuttingDown || !m.options.Interactive || m.agentChild || m.teamWorker != nil || m.Host == nil {
			return cooperative.ErrRestricted
		}
		if err := m.Host.CheckTerminalResumeOnOwner(scope); err != nil {
			return err
		}
		return fn(scope)
	})
	if err == nil {
		err = receipt.Wait()
	}
	return err
}
func (m *MainPrint) ReadTerminalResumeCatalog(ctx context.Context, all bool) (*TerminalResumeCatalog, error) {
	var result *TerminalResumeCatalog
	err := m.terminalResumeRead(ctx, func(scope *cooperative.Scope) error {
		n := m.options.Native.agentScopedBoundary(m.state.Snapshot().OriginalCWD)
		future, err := nativeSkillWork(m, scope, func() (*TerminalResumeCatalog, error) {
			picker, err := NewCLIResumePicker(ctx, n, m.options.Platform, nil)
			if err != nil {
				return nil, err
			}
			// ResumeCommand's loader omits onLoadMore and uses INITIAL_ENRICH_COUNT=50.
			logs, _, err := picker.ReadResumePicker(ctx, &all, 50)
			if err != nil {
				return nil, err
			}
			result := &TerminalResumeCatalog{main: m, picker: picker, logs: logs, all: all}
			branch, err := worktree.NativeRun(ctx, worktree.ProcessRequest{Command: worktree.NativeGitExecutable(n.Environment), Arguments: []string{"rev-parse", "--abbrev-ref", "HEAD"}, CWD: n.CWD, Environment: n.Environment, IgnoreStdin: true})
			if err == nil && branch.Code == 0 {
				result.branch = strings.TrimSpace(branch.Stdout)
			}
			return result, nil
		})
		if err != nil {
			return err
		}
		result, err = future.Await(scope)
		if err != nil {
			return err
		}
		result.currentSID = m.Controller.SessionID()
		kept := []protocol.JSONObject{}
		for _, log := range result.logs {
			if string(log.Get("isSidechain")) != "true" && terminalLogSID(log) != result.currentSID {
				kept = append(kept, log)
			}
		}
		result.logs = kept
		return nil
	})
	return result, err
}
func (m *MainPrint) terminalResumeSelectionOnOwner(scope *cooperative.Scope, c *TerminalResumeCatalog, log protocol.JSONObject, sid string) (*TerminalResumeSelection, error) {
	if c == nil || c.main != m || !printResumeUUID.MatchString(sid) {
		return nil, cooperative.ErrRestricted
	}
	future, err := m.Controller.BeginTerminalFullLogOnOwner(scope, log)
	if err != nil {
		return nil, err
	}
	full, err := future.Await(scope)
	if err != nil {
		return nil, err
	}
	return &TerminalResumeSelection{catalog: c, log: full, sid: sid}, nil
}
func (m *MainPrint) SelectTerminalResume(ctx context.Context, c *TerminalResumeCatalog, index int) (*TerminalResumeSelection, error) {
	var result *TerminalResumeSelection
	err := m.terminalResumeRead(ctx, func(scope *cooperative.Scope) error {
		if c == nil || c.main != m || index < 0 || index >= len(c.logs) {
			return cooperative.ErrRestricted
		}
		var err error
		result, err = m.terminalResumeSelectionOnOwner(scope, c, c.logs[index], terminalLogSID(c.logs[index]))
		return err
	})
	return result, err
}
func (m *MainPrint) ResolveTerminalResumeArgument(ctx context.Context, argument string) (*TerminalResumeSelection, string, error) {
	argument = protocol.TrimJavaScriptString(argument)
	var result *TerminalResumeSelection
	message := ""
	err := m.terminalResumeRead(ctx, func(scope *cooperative.Scope) error {
		n := m.options.Native.agentScopedBoundary(m.state.Snapshot().OriginalCWD)
		future, err := nativeSkillWork(m, scope, func() (*TerminalResumeCatalog, error) {
			picker, err := NewCLIResumePicker(ctx, n, m.options.Platform, nil)
			if err != nil {
				return nil, err
			}
			all := false
			rows, _, err := picker.ReadResumePicker(ctx, &all, 50)
			return &TerminalResumeCatalog{main: m, picker: picker, logs: rows}, err
		})
		if err != nil {
			return err
		}
		c, err := future.Await(scope)
		if err != nil {
			return err
		}
		if len(c.logs) == 0 {
			message = "No conversations found to resume."
			return nil
		}
		if printResumeUUID.MatchString(argument) {
			for _, log := range c.logs {
				if terminalLogSID(log) == argument {
					result, err = m.terminalResumeSelectionOnOwner(scope, c, log, argument)
					return err
				}
			}
			direct, err := m.Controller.BeginTerminalLastLogOnOwner(scope, argument)
			if err != nil {
				return err
			}
			log, err := direct.Await(scope)
			if err != nil {
				return err
			}
			if log.IsObject() {
				result = &TerminalResumeSelection{catalog: c, log: log, sid: argument}
				return nil
			}
		}
		// searchSessionsByCustomTitle does its own fresh, all-stat enrichment, then
		// exact case-insensitive trim and SID dedup. It is not limited to the menu50.
		titles, err := nativeSkillWork(m, scope, func() ([]protocol.JSONObject, error) {
			picker, err := NewCLIResumePicker(ctx, n, m.options.Platform, nil)
			if err != nil {
				return nil, err
			}
			all := false
			rows, more, err := picker.ReadResumePicker(ctx, &all, 50)
			for more && err == nil {
				rows, more, err = picker.ReadResumePicker(ctx, nil, 50)
			}
			if err != nil {
				return nil, err
			}
			query := cases.Lower(language.Und).String(protocol.TrimJavaScriptString(argument))
			matches := []protocol.JSONObject{}
			seen := map[string]bool{}
			for _, log := range rows {
				title := cases.Lower(language.Und).String(protocol.TrimJavaScriptString(log.String("customTitle")))
				sid := terminalLogSID(log)
				if title != "" && title == query && sid != "" && !seen[sid] {
					seen[sid] = true
					matches = append(matches, log)
				}
			}
			return matches, nil
		})
		if err != nil {
			return err
		}
		matches, err := titles.Await(scope)
		if err != nil {
			return err
		}
		if len(matches) == 1 {
			result, err = m.terminalResumeSelectionOnOwner(scope, c, matches[0], terminalLogSID(matches[0]))
			return err
		}
		if len(matches) > 1 {
			message = fmt.Sprintf("Found %d sessions matching %s. Please use /resume to pick a specific session.", len(matches), argument)
		} else {
			message = "Session " + argument + " was not found."
		}
		return nil
	})
	return result, message, err
}
func (m *MainPrint) ResumeTerminalSelection(ctx context.Context, selected *TerminalResumeSelection) (protocol.JSONObject, error) {
	if selected == nil || selected.catalog == nil || selected.catalog.main != m {
		return protocol.JSONObject{}, cooperative.ErrRestricted
	}
	// Source context.resume receives an explicit requested SID separately from
	// the LogOption (whose copied root may belong to the fork's original parent).
	log := selected.log.Clone()
	log.Set("sessionId", quoteMain(selected.sid))
	return m.TerminalResumeLog(ctx, log)
}
func (m *MainPrint) TerminalResumeCrossProject(selected *TerminalResumeSelection) string {
	if selected == nil || selected.catalog == nil || selected.catalog.main != m {
		return ""
	}
	if selected.catalog.picker.IsCrossProject(selected.log, selected.catalog.all) {
		return selected.catalog.picker.CrossProjectCommand(selected.log)
	}
	return ""
}

func (m *MainPrint) PreviewTerminalResume(ctx context.Context, c *TerminalResumeCatalog, index int) (protocol.JSONObject, error) {
	selected, err := m.SelectTerminalResume(ctx, c, index)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	return selected.log.Clone(), nil
}
func (m *MainPrint) RenameTerminalResume(ctx context.Context, c *TerminalResumeCatalog, index int, title string) error {
	return m.terminalResumeRead(ctx, func(scope *cooperative.Scope) error {
		if c == nil || c.main != m || index < 0 || index >= len(c.logs) {
			return cooperative.ErrRestricted
		}
		title = protocol.TrimJavaScriptString(title)
		if title == "" {
			return nil
		}
		log := c.logs[index]
		return m.Controller.SaveTerminalResumeTitleOnOwner(scope, terminalLogSID(log), log.String("fullPath"), quoteMain(title))
	})
}
