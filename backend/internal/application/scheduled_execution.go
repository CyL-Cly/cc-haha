package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/scheduler"
)

func scheduledExecutor(base Options) scheduler.Executor {
	return func(ctx context.Context, request scheduler.ExecutionRequest, emit func(json.RawMessage)) (scheduler.ExecutionResult, error) {
		o := base
		o.WorkDir = request.WorkDir
		o.Environment = make(map[string]string, len(request.Environment))
		for key, value := range request.Environment {
			o.Environment[key] = value
		}
		o.RuntimeSelection = nil
		o.Arguments.Resume = ""
		o.Arguments.ForkSession = false
		o.Arguments.SessionID = request.SessionID
		o.Arguments.Model = strings.TrimFunc(request.Task.String("model"), scheduledSpace)
		o.Arguments.PermissionMode = "bypassPermissions"
		o.Arguments.DangerouslySkipPermissions = true
		o.NonInteractivePermissions = true
		var stderr bytes.Buffer
		o.Stderr = &stderr
		o.OnSDK = func(row json.RawMessage) error { emit(append(json.RawMessage(nil), row...)); return nil }
		// Environment has already passed cron's own provider selection. Creating
		// the native process explicitly prevents a second desktop active-provider
		// resolver from changing the task's undefined/null/string semantics.
		var err error
		if o.NativeFactory != nil {
			o.Native, err = o.NativeFactory(o.Environment, o.WorkDir, o.Platform)
		} else {
			o.Native, err = clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: o.Environment, CWD: o.WorkDir, Platform: o.Platform})
		}
		if err != nil {
			return scheduler.ExecutionResult{ExitCode: 1, Stderr: stderr.String() + err.Error() + "\n"}, nil
		}
		s, err := NewSession(ctx, o)
		if err != nil {
			return scheduler.ExecutionResult{ExitCode: 1, Stderr: stderr.String() + err.Error() + "\n"}, nil
		}
		err = s.SubmitSDK(ctx, request.Input)
		err = errors.Join(err, s.Close(context.WithoutCancel(ctx)))
		code := s.process.Host.ExitCode()
		// Source stops a scheduled child with SIGTERM. The shared SDK handles
		// model cancellation as a normal yield, so retain the scheduler's process
		// outcome rather than misreporting that yield as a completed task.
		if ctx.Err() != nil {
			code = 143
		}
		if err != nil && code == 0 {
			code = 1
		}
		if err != nil {
			stderr.WriteString(err.Error() + "\n")
		}
		return scheduler.ExecutionResult{ExitCode: code, Stderr: stderr.String()}, nil
	}
}

func scheduledSpace(r rune) bool {
	return r >= 9 && r <= 13 || r == 0x20 || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000 || r == 0xfeff
}
