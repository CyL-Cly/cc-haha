package authhelper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type ProcessOptions struct {
	CWD         string
	Environment map[string]string
}

// Execute is execa(shell:true,reject:false)'s credential-command primitive.
// The caller supplies the actual trusted process cwd and environment snapshot.
func Execute(ctx context.Context, command string, options ProcessOptions) (string, error) {
	cmd, err := shellCommand(ctx, command)
	if err != nil {
		return "", err
	}
	cmd.Dir = options.CWD
	keys := []string{}
	for key := range options.Environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	cmd.Env = []string{}
	for _, key := range keys {
		cmd.Env = append(cmd.Env, key+"="+options.Environment[key])
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err != nil {
		why := ""
		var exit *exec.ExitError
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			why = "timed out"
		} else if errors.As(err, &exit) {
			why = fmt.Sprintf("exited %d", exit.ExitCode())
		} else {
			return "", err
		}
		text := strings.TrimFunc(string(protocol.DecodeJavaScriptUTF8(stderr.Bytes())), jsWhitespace)
		if text != "" {
			why += ": " + text
		}
		return "", errors.New(why)
	}
	text := strings.TrimFunc(string(protocol.DecodeJavaScriptUTF8(stdout.Bytes())), jsWhitespace)
	if text == "" {
		return "", errors.New("did not return a value")
	}
	return text, nil
}
