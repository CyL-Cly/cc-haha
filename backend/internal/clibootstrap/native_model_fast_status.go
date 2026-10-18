package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/runtimecleanup"
	"github.com/NanmiCoder/cc-haha/backend/internal/webfetch"
	"net/http"
)

// This distribution's original constants/keys.ts returns an empty GrowthBook
// client key. getFeatureValue_CACHED_MAY_BE_STALE therefore returns defaults,
// including the fast startup kill switch and refresh throttle. Persisted stale
// GrowthBook values cannot enable these unavailable source branches.
// Startup starts the original background prefetch without awaiting it. The
// real Owner owns auth, memo state, persistence and the completion callback;
// only the current HTTP operation parks on an external worker.
func (m *MainPrint) beginNativeModelFastStatus(scope *cooperative.Scope) error {
	if !scope.BelongsTo(m.Owner) {
		return cooperative.ErrRestricted
	}
	if err := m.initializeNativeModelCommandState(scope); err != nil {
		return err
	}
	env := m.nativeOpenAIEnvironment()
	if pickerTrue(env["CLAUDE_CODE_DISABLE_FAST_MODE"]) || env["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] != "" {
		return nil
	}
	global, err := m.Global.Get()
	if err != nil {
		return err
	}
	if pickerTrue(env["CLAUDE_CODE_SIMPLE"]) {
		return nil
	}
	state := m.modelCommandRuntime
	if state.statusDone != nil {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	state.statusCancel = cancel
	state.statusComplete, err = ownerasync.NewOwnerFuture[struct{}](m.Owner)
	if err != nil {
		cancel()
		return err
	}
	state.statusDone = state.statusComplete.Done()
	finish := func(next *cooperative.Scope, failure error) error {
		if failure != nil && !errors.Is(failure, context.Canceled) {
			return state.statusComplete.Reject(next, failure)
		}
		return state.statusComplete.Resolve(next, struct{}{})
	}
	token, key, err := m.nativeModelFastAuth(ctx, scope)
	if err != nil {
		return finish(scope, err)
	}
	if token == "" && key == "" {
		state.orgEnabled = env["USER_TYPE"] == "ant" || global != nil && string(global.Get("penguinModeOrgEnabled")) == "true"
		if !state.orgEnabled {
			state.orgReason = "preference"
		}
		return finish(scope, nil)
	}
	_, err = m.Cleanup.Register(scope, runtimecleanup.NewCallback(func(next *cooperative.Scope) (*runtimecleanup.Future, error) {
		cancel()
		return nativeSkillWork(m, next, func() (struct{}, error) { m.joinNativeModelFastWorkers(); return struct{}{}, nil })
	}))
	if err != nil {
		cancel()
		return finish(scope, err)
	}
	return m.startNativeModelFastHTTP(scope, ctx, token, key, true, finish)
}
func (m *MainPrint) nativeModelFastWork(scope *cooperative.Scope, ctx context.Context, work func() ([]byte, error), after func(*cooperative.Scope, []byte, error) error) error {
	state := m.modelCommandRuntime
	state.jobsMu.Lock()
	if ctx.Err() != nil {
		state.jobsMu.Unlock()
		return after(scope, nil, context.Cause(ctx))
	}
	done := make(chan struct{})
	state.jobs = append(state.jobs, done)
	state.jobsMu.Unlock()
	future, err := nativeSkillWork(m, scope, func() ([]byte, error) { defer close(done); return work() })
	if err != nil {
		close(done)
		return after(scope, nil, err)
	}
	_, err = future.Then(scope, func(next *cooperative.Scope, raw []byte, failure error) error { return after(next, raw, failure) })
	return err
}
func (m *MainPrint) joinNativeModelFastWorkers() {
	state := m.modelCommandRuntime
	if state == nil {
		return
	}
	state.jobsMu.Lock()
	jobs := append([]<-chan struct{}{}, state.jobs...)
	state.jobsMu.Unlock()
	for _, done := range jobs {
		<-done
	}
}
func (m *MainPrint) closeNativeModelFastStatus() {
	state := m.modelCommandRuntime
	if state == nil || state.statusCancel == nil {
		return
	}
	state.statusCancel()
	m.joinNativeModelFastWorkers()
	_, _ = state.statusComplete.Wait(context.Background())
}

func nativeModelProfileToken(tokens *openaiauth.Value) string {
	if tokens == nil {
		return ""
	}
	var scopes []string
	_ = json.Unmarshal(tokens.Get("scopes"), &scopes)
	for _, s := range scopes {
		if s == "user:profile" {
			return tokens.Object.String("accessToken")
		}
	}
	return ""
}
func (m *MainPrint) nativeModelFastAuth(ctx context.Context, scope *cooperative.Scope) (string, string, error) {
	owned, err := m.openAIAuth.owned(ctx, scope)
	if err != nil {
		return "", "", err
	}
	env := m.nativeOpenAIEnvironment()
	key, _, err := m.openAIAuth.apiKeyWithSource(owned, env, pickerTrue(env["CLAUDE_CODE_SIMPLE"]), !m.options.Interactive)
	if err != nil {
		return "", "", err
	}
	tokens, err := m.openAIAuth.claudeTokensFor(owned, env, pickerTrue(env["CLAUDE_CODE_SIMPLE"]))
	return nativeModelProfileToken(tokens), key, err
}

type nativeFastStatusResponse struct {
	Data   protocol.JSONObject
	Status int
	Text   string
	String bool
}

func (m *MainPrint) startNativeModelFastHTTP(scope *cooperative.Scope, ctx context.Context, token, key string, recoverAuth bool, finish func(*cooperative.Scope, error) error) error {
	env := m.nativeOpenAIEnvironment()
	config, err := m.nativeModelFastConfig(env)
	if err != nil {
		return finish(scope, err)
	}
	client, err := webfetch.NewHTTPClient(env, m.tls)
	if err != nil {
		return finish(scope, err)
	}
	return m.nativeModelFastWork(scope, ctx, func() ([]byte, error) {
		request, err := http.NewRequestWithContext(ctx, "GET", config.BaseAPIURL+"/api/claude_code_penguin_mode", nil)
		if err != nil {
			return nil, err
		}
		request.Header.Set("Accept", "application/json, text/plain, */*")
		request.Header.Set("Accept-Encoding", "gzip, compress, deflate, br")
		request.Header.Set("User-Agent", "axios/1.15.2")
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
			request.Header.Set("anthropic-beta", "oauth-2025-04-20")
		} else {
			request.Header.Set("x-api-key", key)
		}
		response, err := client.Do(request)
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()
		body, err := webfetch.ReadAxiosResponseBody(response)
		if err != nil {
			return nil, err
		}
		body = bytes.TrimPrefix(body, []byte{0xef, 0xbb, 0xbf})
		decoded := protocol.DecodeJavaScriptUTF8(body)
		var data protocol.JSONObject
		isString := !json.Valid(decoded)
		text := string(decoded)
		if json.Valid(decoded) {
			_ = data.UnmarshalJSON(decoded)
			var v string
			if json.Unmarshal(decoded, &v) == nil {
				isString = true
				text = v
			}
		}
		return json.Marshal(nativeFastStatusResponse{Data: data, Status: response.StatusCode, Text: text, String: isString})
	}, func(next *cooperative.Scope, raw []byte, failure error) error {
		if ctx.Err() != nil {
			return finish(next, nil)
		}
		var status nativeFastStatusResponse
		if failure == nil {
			failure = json.Unmarshal(raw, &status)
		}
		authError := status.Status == 401 || status.Status == 403 && status.String && bytes.Contains([]byte(status.Text), []byte("OAuth token has been revoked"))
		if failure == nil && recoverAuth && authError && token != "" {
			return m.startNativeModelFast401(next, ctx, token, func(after *cooperative.Scope) error {
				if ctx.Err() != nil {
					return finish(after, nil)
				}
				current, _, err := m.nativeModelFastAuth(ctx, after)
				if err != nil {
					return finish(after, err)
				}
				if current == "" && key == "" {
					return finish(after, m.completeNativeModelFastStatus(after, nativeFastStatusResponse{}, errors.New("No auth available")))
				}
				return m.startNativeModelFastHTTP(after, ctx, current, key, false, finish)
			})
		}
		return finish(next, m.completeNativeModelFastStatus(next, status, failure))
	})
}
func (m *MainPrint) completeNativeModelFastStatus(scope *cooperative.Scope, status nativeFastStatusResponse, failure error) error {
	state := m.modelCommandRuntime
	env := m.nativeOpenAIEnvironment()
	global, err := m.Global.Get()
	if err != nil {
		return err
	}
	if failure != nil || status.Status < 200 || status.Status >= 300 {
		state.orgEnabled = env["USER_TYPE"] == "ant" || global != nil && string(global.Get("penguinModeOrgEnabled")) == "true"
		if !state.orgEnabled {
			state.orgReason = "network_error"
		}
		return nil
	}
	enabled := modelCommandTruthy(status.Data.Get("enabled"))
	previous := global != nil && string(global.Get("penguinModeOrgEnabled")) == "true"
	state.orgEnabled = enabled
	state.orgReason = status.Data.String("disabled_reason")
	if !enabled && state.orgReason == "" {
		state.orgReason = "preference"
	}
	if previous != enabled {
		if !enabled {
			if err = m.nativeModelFastDisablePreference(); err != nil {
				return err
			}
			if err = m.Host.AcceptInternalFastModeSettings(scope); err != nil {
				return err
			}
		}
		if err = m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
			next := current.Clone()
			if value := status.Data.Get("enabled"); value != nil {
				next.Set("penguinModeOrgEnabled", value)
			} else {
				next.Delete("penguinModeOrgEnabled")
			}
			return &next, nil
		}); err != nil {
			return err
		}
		return m.nativeModelFastOrgNotice(scope, enabled)
	}
	return nil
}

func (m *MainPrint) nativeModelFastConfig(env map[string]string) (desktopoauth.Config, error) {
	if m.options.NativeModelFastConfig != nil {
		return *m.options.NativeModelFastConfig, nil
	}
	return desktopoauth.ResolveConfig(env)
}
