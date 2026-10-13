package mcpheaders

import (
	"bytes"
	"context"
	"os/exec"
	"sort"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func NativeRun(ctx context.Context, request Request) (ProcessResult, error) {
	active, cancel := context.WithTimeout(ctx, request.Timeout)
	defer cancel()
	cmd := nativeCommand(active, request.Command, request.Environment)
	cmd.Dir = request.CWD
	cmd.Env = []string{}
	keys := []string{}
	for key := range request.Environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		cmd.Env = append(cmd.Env, key+"="+request.Environment[key])
	}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &bytes.Buffer{}
	e := cmd.Run()
	code := 0
	if e != nil {
		code = 1
		if exit, ok := e.(*exec.ExitError); ok {
			code = exit.ExitCode()
		}
	}
	if ctx.Err() != nil {
		return ProcessResult{}, context.Cause(ctx)
	}
	return ProcessResult{Stdout: protocol.TrimJavaScriptString(string(protocol.DecodeJavaScriptUTF8(stdout.Bytes()))), Code: code}, nil
}
