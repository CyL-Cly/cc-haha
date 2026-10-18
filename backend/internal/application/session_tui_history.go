package application

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

// Source establishes initialMessages before the worker/inbox effect starts.
// Capture the same restored Controller while initialization still owns it.
func (s *Session) captureInitialTUIHistoryOnOwner(scope *cooperative.Scope) error {
	if !scope.BelongsTo(s.process.Owner) {
		return cooperative.ErrRestricted
	}
	records := s.process.Controller.History()
	environment := make(map[string]string, len(s.native.Environment))
	for key, value := range s.native.Environment {
		environment[key] = value
	}
	s.mu.Lock()
	s.initialTUIRecords, s.initialTUIEnvironment = records, environment
	s.mu.Unlock()
	return nil
}

// Presentation reads only the immutable startup snapshot, so a pane's actual
// worker may already be awaiting OS/model work on the original Owner. It never
// replays SDK output, submits a turn, allocates UUIDs or records messages.
func (s *Session) initialTUIHistory(ctx context.Context) ([]protocol.JSONObject, bool, error) {
	s.mu.Lock()
	records := make([]protocol.JSONObject, len(s.initialTUIRecords))
	for i, record := range s.initialTUIRecords {
		records[i] = record.Clone()
	}
	environment := make(map[string]string, len(s.initialTUIEnvironment))
	for key, value := range s.initialTUIEnvironment {
		environment[key] = value
	}
	s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if s.terminalInteractive {
		fullscreen, err := s.process.ReadTerminalFullscreen(ctx)
		return records, fullscreen, err
	}
	fullscreen := tui.FullscreenEnabled(environment, func() (string, error) {
		probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		command := exec.CommandContext(probeCtx, "tmux", "display-message", "-p", "#{client_control_mode}")
		command.Env = make([]string, 0, len(environment))
		for key, value := range environment {
			command.Env = append(command.Env, key+"="+value)
		}
		command.Dir = s.native.CWD
		output, err := command.Output()
		return string(output), err
	})
	return records, fullscreen, nil
}

// ResumeHint captures the original live SID/title while its Owner still exists.
// It is presentation only; a nonexistent or disabled transcript has no hint.
func (s *Session) ResumeHint(ctx context.Context) (string, error) {
	var hint string
	receipt, err := s.process.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		persists, err := s.process.Controller.PersistsOnOwner(scope)
		if err != nil || !persists {
			return err
		}
		writer := s.process.Controller.Writer()
		snapshot := writer.Snapshot()
		path := snapshot.String("path")
		if path == "" {
			path, err = writer.ComputedTranscriptPath(scope)
			if err != nil {
				return err
			}
		}
		if _, err := os.Stat(path); err != nil {
			return nil
		}
		argument := s.process.Controller.SessionID()
		if title := obj(snapshot.Get("metadata")).String("customTitle"); title != "" {
			argument = `"` + strings.ReplaceAll(strings.ReplaceAll(title, `\`, `\\`), `"`, `\"`) + `"`
		}
		// Source spells its original distribution's launcher "claude". This
		// distribution installs only claude-haha (scripts/cli/install.cjs), so
		// bind that known Go entry rather than guessing an ambient PATH alias.
		hint = fmt.Sprintf("\nResume this session with:\nclaude-haha --resume %s\n", argument)
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	return hint, err
}
