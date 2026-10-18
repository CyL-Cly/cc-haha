package application

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/ccrsessions"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

type cliRemoteStartup struct {
	process    *clibootstrap.MainPrint
	service    *ccrsessions.Service
	transcript string
	created    *ccrsessions.Created
	prompt     string
}

func (s *cliRemoteStartup) Close() {
	if s.service != nil {
		s.service.Close()
	}
	if s.process != nil {
		s.process.Close()
	}
	if s.transcript != "" {
		_ = os.Remove(s.transcript)
	}
}

type cliRemotePicker struct {
	service *ccrsessions.Service
	rows    []protocol.JSONObject
	cwd     string
}

func (p *cliRemotePicker) CurrentDirectory() string { return p.cwd }
func (p *cliRemotePicker) ReadResumePicker(ctx context.Context, _ *bool, _ int) ([]protocol.JSONObject, bool, error) {
	if p.rows != nil {
		return p.rows, false, nil
	}
	sessions, err := p.service.ListSessions(ctx)
	if err != nil {
		return nil, false, err
	}
	p.rows = []protocol.JSONObject{}
	for i, session := range sessions {
		title := session.String("title")
		if title == "" {
			title = "Untitled"
		}
		stamp, _ := time.Parse(time.RFC3339Nano, session.String("updated_at"))
		row := protocol.NewJSONObject(protocol.JSONField{Name: "sessionId", Value: raw(session.String("id"))}, protocol.JSONField{Name: "fullPath", Value: raw(session.String("id"))}, protocol.JSONField{Name: "customTitle", Value: raw(title)}, protocol.JSONField{Name: "firstPrompt", Value: raw(title)}, protocol.JSONField{Name: "projectPath", Value: raw(p.cwd)}, protocol.JSONField{Name: "modified", Value: raw(stamp.UnixMilli())}, protocol.JSONField{Name: "messageCount", Value: raw(0)}, protocol.JSONField{Name: "value", Value: raw(i)})
		p.rows = append(p.rows, row)
	}
	return p.rows, false, nil
}

type cliRepositoryPicker struct {
	rows []protocol.JSONObject
	cwd  string
}

func (p *cliRepositoryPicker) CurrentDirectory() string { return p.cwd }
func (p *cliRepositoryPicker) ReadResumePicker(context.Context, *bool, int) ([]protocol.JSONObject, bool, error) {
	return p.rows, false, nil
}

// The API phase owns one startup-file promise and ingress credential cache.
// Teleport then transfers both into the local application instead of creating
// a worker RemoteIO transport or falling back to the TypeScript CLI.
func prepareCLIRemoteStartup(ctx context.Context, c Command, o *Options, in io.Reader, out io.Writer, env []string) (Command, *cliRemoteStartup, bool, error) {
	a := c.Arguments
	a.SDKURL = ""
	process, err := clibootstrap.NewMainPrint(clibootstrap.MainPrintOptions{ObserveModelFailure: o.ObserveModelFailure, Arguments: a, Native: o.Native, PID: os.Getpid(), Version: o.Version, Platform: o.Platform, Input: strings.NewReader(""), Output: io.Discard, Error: o.Stderr, Interactive: !c.Print})
	if err != nil {
		return c, nil, false, err
	}
	startup := &cliRemoteStartup{process: process}
	failed := func(err error) (Command, *cliRemoteStartup, bool, error) { startup.Close(); return c, nil, false, err }
	if err := process.StartSession(ctx); err != nil {
		return failed(err)
	}
	service, err := process.NewCLICCRSessions(ctx)
	if err != nil {
		return failed(err)
	}
	startup.service = service
	if !c.Print {
		allowed, err := service.PolicyRemoteAllowed(ctx)
		if err != nil {
			return failed(err)
		}
		if !allowed {
			return failed(errors.New("Error: Remote sessions are disabled by your organization's policy."))
		}
	}
	if c.Arguments.Remote != nil && !c.Print {
		created, err := service.CreateRemote(ctx, *c.Arguments.Remote)
		if err != nil {
			return failed(err)
		}
		if service.RemoteBackendEnabled() {
			startup.created, startup.prompt = &created, *c.Arguments.Remote
			c.Arguments.SessionID = created.ID
			c.Prompt = ""
			return c, startup, false, nil
		}
		_, err = fmt.Fprintf(out, "Created remote session: %s\nView: %s?m=0\nResume with: claude --teleport %s\n", created.Title, ccrsessions.RemoteSessionURL(o.Native.Environment, created.ID), created.ID)
		return c, startup, true, err
	}
	sid := c.Arguments.Teleport
	if sid == "" && !c.Print {
		allowed, err := service.PolicyRemoteAllowed(ctx)
		if err != nil {
			return failed(err)
		}
		if !allowed {
			return failed(errors.New("Remote sessions are disabled by your organization's policy."))
		}
		selected, err := tui.RunResumePicker(ctx, &cliRemotePicker{service: service, cwd: o.Native.CWD}, tui.ResumePickerOptions{Input: in, Output: out, Environment: env})
		if err != nil {
			return failed(err)
		}
		if selected.Cancelled || !selected.Log.IsObject() {
			return c, startup, true, nil
		}
		sid = selected.Log.String("fullPath")
	}
	if !c.Print && sid != "" {
		// Source can switch to a known checkout before dirty-Git validation.
		session, err := service.FetchSession(ctx, sid)
		if err != nil {
			return failed(err)
		}
		validation := service.ValidateRepository(ctx, session)
		if validation.Status == "mismatch" || validation.Status == "not_in_repo" {
			paths, err := process.KnownCLIRepositoryPaths(ctx, validation.SessionRepo)
			if err != nil {
				return failed(err)
			}
			if len(paths) == 0 {
				return failed(validation.Error(sid))
			}
			rows := []protocol.JSONObject{}
			for i, path := range paths {
				rows = append(rows, protocol.NewJSONObject(protocol.JSONField{Name: "fullPath", Value: raw(path)}, protocol.JSONField{Name: "customTitle", Value: raw(path)}, protocol.JSONField{Name: "projectPath", Value: raw(o.Native.CWD)}, protocol.JSONField{Name: "value", Value: raw(i)}, protocol.JSONField{Name: "messageCount", Value: raw(0)}))
			}
			selected, err := tui.RunResumePicker(ctx, &cliRepositoryPicker{rows: rows, cwd: o.Native.CWD}, tui.ResumePickerOptions{Input: in, Output: out, Environment: env})
			if err != nil {
				return failed(err)
			}
			if selected.Cancelled || !selected.Log.IsObject() {
				return c, startup, true, nil
			}
			path, err := filepath.EvalSymlinks(selected.Log.String("fullPath"))
			if err != nil {
				return failed(err)
			}
			if err := service.UseDirectory(path); err != nil {
				return failed(err)
			}
			verified := service.ValidateRepository(ctx, session)
			if err := verified.Error(sid); err != nil {
				_ = process.ForgetKnownCLIRepositoryPath(ctx, validation.SessionRepo, selected.Log.String("fullPath"))
				return failed(err)
			}
			o.Native = clibootstrap.CLIWorkingDirectory(o.Native, path)
			o.WorkDir = path
		}
	}
	teleported, err := service.Teleport(ctx, sid, !c.Print)
	if err != nil {
		return failed(err)
	}
	file, err := os.CreateTemp(o.Native.Temp, "ccr-teleport-*.jsonl")
	if err != nil {
		return failed(err)
	}
	startup.transcript = file.Name()
	write := func(row protocol.JSONObject) error {
		data, err := row.MarshalJSON()
		if err != nil {
			return err
		}
		_, err = file.Write(append(data, '\n'))
		return err
	}
	for _, message := range teleported.Messages {
		if err := write(message); err != nil {
			_ = file.Close()
			return failed(err)
		}
	}
	prior := ""
	if len(teleported.Messages) > 0 {
		prior = teleported.Messages[len(teleported.Messages)-1].String("uuid")
	}
	uuid := newID()
	notice := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw("user")}, protocol.JSONField{Name: "uuid", Value: raw(uuid)}, protocol.JSONField{Name: "parentUuid", Value: raw(prior)}, protocol.JSONField{Name: "sessionId", Value: raw(sid)}, protocol.JSONField{Name: "isMeta", Value: raw(true)}, protocol.JSONField{Name: "message", Value: raw(map[string]string{"role": "user", "content": "This session is being continued from another machine. Application state may have changed. The updated working directory is " + o.Native.CWD})})
	notice.Set("timestamp", raw(time.Now().UTC().Format(time.RFC3339Nano)))
	if err := write(notice); err != nil {
		_ = file.Close()
		return failed(err)
	}
	content, severity := "Session resumed", "suggestion"
	if teleported.BranchError != nil {
		content, severity = "Session resumed without branch: "+teleported.BranchError.Error(), "warning"
	}
	notice = protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw("system")}, protocol.JSONField{Name: "uuid", Value: raw(newID())}, protocol.JSONField{Name: "parentUuid", Value: raw(uuid)}, protocol.JSONField{Name: "sessionId", Value: raw(sid)}, protocol.JSONField{Name: "content", Value: raw(content)}, protocol.JSONField{Name: "subtype", Value: raw("informational")}, protocol.JSONField{Name: "level", Value: raw(severity)})
	notice.Set("timestamp", raw(time.Now().UTC().Format(time.RFC3339Nano)))
	if err := write(notice); err != nil {
		_ = file.Close()
		return failed(err)
	}
	if err := file.Close(); err != nil {
		return failed(err)
	}
	c.Arguments.Resume, c.Arguments.ResumeWithoutValue, c.Arguments.ForkSession = startup.transcript, false, true
	c.Arguments.Teleport, c.Arguments.TeleportWithoutValue = "", false
	c.Arguments.Remote = nil
	c.Arguments.FileSpecs = nil
	o.SessionIngressAuth = process.CLIIngressAuth()
	return c, startup, false, nil
}
