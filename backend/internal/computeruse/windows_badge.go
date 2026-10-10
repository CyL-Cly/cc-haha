package computeruse

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io"
	"math"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type windowsBadge struct {
	command *exec.Cmd
	input   io.WriteCloser
	ready   chan struct{}
	done    chan struct{}
	once    sync.Once
}

func (client *windowsClient) showBadge(ctx context.Context, command string, payload json.RawMessage) {
	client.badgeMu.Lock()
	defer client.badgeMu.Unlock()
	if client.ctx.Err() != nil || ctx.Err() != nil {
		return
	}
	badge := client.badge
	if badge != nil {
		select {
		case <-badge.done:
			client.badge = nil
			badge = nil
		default:
		}
	}
	if badge == nil {
		cmd := exec.CommandContext(client.ctx, client.service.windowsPythonPath(), filepath.Join(client.service.windowsRuntimeRoot(), "win_cursor_badge.py"))
		configureNativeProcess(cmd)
		configureWindowsBadge(cmd)
		cmd.Dir = client.service.options.WorkDir
		cmd.WaitDelay = time.Second
		keys := []string{}
		for key := range client.service.options.Environment {
			if key != "CC_HAHA_COMPUTER_USE_INPUT_TAG" {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			cmd.Env = append(cmd.Env, key+"="+client.service.options.Environment[key])
		}
		cmd.Env = append(cmd.Env, "CC_HAHA_COMPUTER_USE_INPUT_TAG="+client.tag)
		input, err := cmd.StdinPipe()
		if err != nil {
			return
		}
		out, err := cmd.StdoutPipe()
		if err != nil {
			input.Close()
			return
		}
		cmd.Stderr = io.Discard
		badge = &windowsBadge{command: cmd, input: input, ready: make(chan struct{}), done: make(chan struct{})}
		if err = cmd.Start(); err != nil {
			input.Close()
			out.Close()
			return
		}
		client.badge = badge
		go func() {
			defer out.Close()
			var text strings.Builder
			buf := make([]byte, 4096)
			for {
				n, err := out.Read(buf)
				if n > 0 {
					text.Write(buf[:n])
					if strings.Contains(text.String(), "READY\n") || trim(text.String()) == "READY" {
						badge.once.Do(func() { close(badge.ready) })
					}
				}
				if err != nil {
					return
				}
			}
		}()
		go func() { _ = cmd.Wait(); badge.once.Do(func() { close(badge.ready) }); close(badge.done) }()
	}
	timer := time.NewTimer(1500 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-badge.ready:
	case <-timer.C:
	case <-ctx.Done():
		return
	case <-client.ctx.Done():
		return
	}
	fields := []protocol.JSONField{{Name: "command", Value: encode(command)}, {Name: "payload", Value: payload}}
	object, _ := decodeObject(payload)
	if pid, err := optionalNumber(object["pid"], "pid"); err == nil && pid != nil && *pid > 0 && *pid <= 9007199254740991 && math.Trunc(*pid) == *pid {
		fields = append(fields, protocol.JSONField{Name: "targetPid", Value: object["pid"]})
	}
	data, _ := protocol.NewJSONObject(fields...).MarshalJSON()
	data = append(data, '\n')
	_, _ = badge.input.Write(data)
}
func (client *windowsClient) hideBadge(ctx context.Context) error {
	client.badgeMu.Lock()
	badge := client.badge
	client.badge = nil
	client.badgeMu.Unlock()
	if badge == nil {
		return nil
	}
	_ = badge.input.Close()
	if badge.command.Process != nil {
		_ = stopWindowsBadge(badge.command)
	}
	select {
	case <-badge.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
