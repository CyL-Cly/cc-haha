package clibootstrap

import (
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/NanmiCoder/cc-haha/backend/internal/commandnames"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/releasenotes"
	"github.com/NanmiCoder/cc-haha/backend/internal/resumestate"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

type nativeTerminalExportStartup struct{ activities []tui.ExportActivity }

var terminalExportCoerceVersion = regexp.MustCompile(`(?:^|[^0-9])([0-9]{1,16})(?:\.([0-9]{1,16}))?(?:\.([0-9]{1,16}))?(?:$|[^0-9])`)

func terminalExportBaseVersion(value string) string {
	match := terminalExportCoerceVersion.FindStringSubmatch(value)
	if len(match) == 0 {
		return ""
	}
	parts := [3]string{}
	for i := range parts {
		if match[i+1] == "" {
			parts[i] = "0"
			continue
		}
		if len(match[i+1]) > 1 && match[i+1][0] == '0' {
			return ""
		}
		number, err := strconv.ParseUint(match[i+1], 10, 64)
		if err != nil || number > 9007199254740991 {
			return ""
		}
		parts[i] = strconv.FormatUint(number, 10)
	}
	return parts[0] + "." + parts[1] + "." + parts[2]
}

func terminalExportHasRelease(content, current, last string) bool {
	previous, actual := terminalExportBaseVersion(last), terminalExportBaseVersion(current)
	if previous != "" {
		if actual == "" {
			return false
		}
		comparison, err := releasenotes.Compare(actual, previous)
		if err != nil || comparison <= 0 {
			return false
		}
	}
	entries := releasenotes.All(content)
	for _, entry := range entries {
		if previous == "" {
			return true
		}
		if comparison, err := releasenotes.Compare(entry.Version, previous); err == nil && comparison > 0 {
			return true
		}
	}
	return false
}

// Source setup warms the same cached changelog before any fresh Logo tree,
// and loads recent logs only when there are unseen release notes. SIMPLE skips
// this producer completely; detached agents never run a second CLI setup.
func (m *MainPrint) initializeTerminalExportStartup(scope *cooperative.Scope) error {
	n := m.options.Native
	if m.terminalExportStartup != nil || n == nil || !n.NativeProcess || m.options.DetachedAgent != nil || nativeEnvTruthy(n.Environment["CLAUDE_CODE_SIMPLE"]) {
		return nil
	}
	if err := m.ensureNativeReleaseNotes(scope); err != nil {
		return err
	}
	global, err := m.Global.Get()
	if err != nil {
		return err
	}
	last := global.String("lastReleaseNotesSeen")
	currentSID := m.state.SessionID()
	future, err := nativeSkillWork(m, scope, func() ([]tui.ExportActivity, error) {
		content := m.nativeLocalCommands.releaseNotes.Preload(!m.options.Interactive, n.Environment["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] != "", last != m.options.Version)
		activities := []tui.ExportActivity{}
		if !terminalExportHasRelease(content, m.options.Version, last) {
			return activities, nil
		}
		names, err := commandnames.Set(n.Environment)
		if err != nil {
			return nil, err
		}
		config := n.ConfigHomeDirectory()
		if config == "" {
			config = filepath.Join(n.Home, ".claude")
		}
		if !filepath.IsAbs(config) {
			config = filepath.Join(n.CWD, config)
		}
		directory := filepath.Join(config, "projects", projectpath.Sanitize(m.state.Snapshot().OriginalCWD))
		logs := resumestate.ReadRecentActivity(mainResumeFiles{n: n}, directory, currentSID, func(path string) (resumestate.Stat, error) {
			info, err := n.stat(path)
			if err != nil {
				return resumestate.Stat{}, err
			}
			return resumestate.Stat{Size: info.Size(), Modified: nativeResumeDateMillis(info.ModTime()), Birth: nativeResumeBirthMillis(path, info)}, nil
		}, names)
		for _, log := range logs {
			text := log.String("summary")
			if text == "" || text == "No prompt" {
				text = log.String("firstPrompt")
			}
			activities = append(activities, tui.ExportActivity{Description: text, ModifiedMillis: terminalExportNumber(log.Get("modified"))})
		}
		return activities, nil
	})
	if err != nil {
		return err
	}
	activities, err := future.Await(scope)
	if err != nil {
		return err
	}
	m.terminalExportStartup = &nativeTerminalExportStartup{activities: activities}
	if m.options.Interactive {
		return m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
			next := current.Clone()
			next.Set("numStartups", rawMain(terminalExportNumber(current.Get("numStartups"))+1))
			return &next, nil
		})
	}
	return nil
}
