package cloudauth

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// RunConfiguredCommand is the original configured auth-command OS primitive.
// It never synthesizes aws/gcloud commands or borrows the ambient environment.
func RunConfiguredCommand(ctx context.Context, command string, timeout time.Duration, cwd string, environment map[string]string) (CommandOutput, error) {
	return runConfiguredCommand(ctx, command, timeout, cwd, environment, nil, nil)
}

type authCommandWriter struct {
	output  *bytes.Buffer
	publish func(string)
}

func (writer authCommandWriter) Write(data []byte) (int, error) {
	n, err := writer.output.Write(data)
	if writer.publish != nil {
		value := strings.TrimFunc(string(protocol.DecodeJavaScriptUTF8(data)), jsSpace)
		if value != "" {
			writer.publish(value)
		}
	}
	return n, err
}

// RunAuthRefresh streams the actual process chunks, before its completion,
// retaining failed output and clearing it on success like refreshAws/GcpAuth.
func RunAuthRefresh(ctx context.Context, command string, timeout time.Duration, cwd string, environment map[string]string, manager *AuthStatusManager) (CommandOutput, error) {
	if manager == nil {
		return RunConfiguredCommand(ctx, command, timeout, cwd, environment)
	}
	manager.Start()
	output, err := runConfiguredCommand(ctx, command, timeout, cwd, environment, manager.AddOutput, manager.SetError)
	manager.End(err == nil && output.ExitCode == 0)
	return output, err
}
func runConfiguredCommand(ctx context.Context, command string, timeout time.Duration, cwd string, environment map[string]string, publishOutput, publishError func(string)) (CommandOutput, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	name, args := "/bin/sh", []string{"-c", command}
	if runtime.GOOS == "windows" {
		name = environment["ComSpec"]
		if name == "" {
			name = "cmd.exe"
		}
		args = []string{"/d", "/s", "/c", command}
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = cwd
	keys := []string{}
	for key := range environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	cmd.Env = []string{}
	for _, key := range keys {
		cmd.Env = append(cmd.Env, key+"="+environment[key])
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = authCommandWriter{&stdout, publishOutput}
	cmd.Stderr = authCommandWriter{&stderr, publishError}
	err := cmd.Run()
	output := CommandOutput{Stdout: string(protocol.DecodeJavaScriptUTF8(stdout.Bytes())), Stderr: string(protocol.DecodeJavaScriptUTF8(stderr.Bytes()))}
	if timeout == 0 {
		output.Stdout = strings.TrimRight(output.Stdout, "\r\n")
		output.Stderr = strings.TrimRight(output.Stderr, "\r\n")
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		output.ExitCode = exit.ExitCode()
		return output, nil
	}
	if err != nil {
		return output, err
	}
	if timeout > 0 && (stdout.Len() > 1024*1024 || stderr.Len() > 1024*1024) {
		return output, errors.New("configured auth refresh exceeded original exec maxBuffer")
	}
	// Source execa trims only trailing newlines; the export consumer trims the
	// complete stdout separately when parsing STS JSON.
	return output, nil
}
