package application

import (
	"context"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Foreign SDK authentication belongs to its original desktop session, rather
// than the native ASK's retry mechanism. Recovery updates that transport only.
func (d *DesktopSession) recoverSDKOAuth() error {
	d.mu.Lock()
	engine, recovery, stderr := d.engine, d.options.SDKRecoverOfficialOAuth, d.options.Stderr
	d.mu.Unlock()
	if engine == nil || engine.native == nil {
		return nil
	}
	env := engine.native.Environment
	if env["CLAUDE_CODE_ENTRYPOINT"] != "claude-desktop" || env["CLAUDE_CODE_OAUTH_TOKEN"] == "" {
		return nil
	}
	if recovery == nil {
		return &clibootstrap.MainCapabilityError{Boundary: "dedicated SDK official OAuth 401 recovery"}
	}
	state := d.sdkRuntime()
	state.mu.Lock()
	if state.closed || state.recovering {
		state.mu.Unlock()
		return nil
	}
	if state.oauthToken == "" {
		state.oauthToken = env["CLAUDE_CODE_OAUTH_TOKEN"]
	}
	rejected := state.oauthToken
	state.recovering = true
	done := make(chan struct{})
	state.recoveryDone = done
	payloadDone := state.payloadDone
	state.mu.Unlock()
	go func() {
		defer func() { state.mu.Lock(); state.recovering = false; close(done); state.mu.Unlock() }()
		if payloadDone != nil {
			select {
			case <-payloadDone:
			case <-state.ctx.Done():
				return
			}
		}
		token, err := recovery(state.ctx, rejected)
		if err != nil {
			if stderr != nil {
				fmt.Fprintf(stderr, "[conversationService] recover official OAuth token after 401 failed: %s\n", err)
			}
			return
		}
		state.mu.Lock()
		fresh := !state.closed && token != nil && *token != "" && *token != rejected && state.oauthToken == rejected
		if fresh {
			state.oauthToken = *token
		}
		state.mu.Unlock()
		if fresh {
			_ = d.sendSDK(frame("update_environment_variables", field("variables", map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": *token})))
		}
	}()
	return nil
}
func (d *DesktopSession) closeSDKTransport(ctx context.Context) error {
	d.mu.Lock()
	state := d.sdk
	d.mu.Unlock()
	if state == nil {
		return nil
	}
	state.mu.Lock()
	state.closed = true
	for id, wait := range state.controls {
		delete(state.controls, id)
		wait.result <- sdkControlResult{err: errors.New("CLI session stopped")}
	}
	state.send = nil
	state.outbound = nil
	state.pending = map[string]protocol.JSONObject{}
	state.order = nil
	state.cancel()
	done := state.recoveryDone
	state.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	return nil
}
