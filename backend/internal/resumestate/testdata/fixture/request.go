package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/resumestate"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionresume"
)

type requestInput struct {
	Origin, PromptSource string
	Runtime              provider.SourceRuntime
}

func validateRequest(input *requestInput) error {
	parsed, err := url.Parse(input.Origin)
	if err != nil {
		return err
	}
	if parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" || parsed.Port() == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || input.Origin != os.Getenv("ANTHROPIC_BASE_URL") || os.Getenv("ANTHROPIC_API_KEY") != "offline-fixture-key" || !json.Valid([]byte(input.PromptSource)) {
		return errors.New("Resume state request requires original private loopback inputs")
	}
	return nil
}
func executeRequest(input *requestInput, restored *resumestate.Result, reducer *sessionresume.Reducer, uuid func() string, now func() time.Time, allocations *[]map[string]any) (any, any, error) {
	if restored == nil {
		return nil, nil, errors.New("first request requires successful restoration")
	}
	var sid string
	if err := json.Unmarshal(restored.Metadata.Get("sessionId"), &sid); err != nil || sid == "" {
		return nil, nil, errors.New("first request requires actual restored SID")
	}
	copy := *restored
	appended, err := reducer.AppendUser(restored.Result, json.RawMessage(input.PromptSource))
	if err != nil {
		return nil, nil, err
	}
	copy.Result = appended
	generation := append([]map[string]any{}, (*allocations)...)
	historyBefore := []string{}
	for _, message := range copy.Messages {
		historyBefore = append(historyBefore, source(message))
	}
	env := map[string]string{"ANTHROPIC_BASE_URL": input.Origin, "ANTHROPIC_API_KEY": "offline-fixture-key"}
	contextInput := requestcontext.Context{SystemPrompt: []json.RawMessage{}, Thinking: requestcontext.Thinking{Type: "disabled"}, Options: requestcontext.Options{Model: "fixture-model", QuerySource: "sdk", IsNonInteractiveSession: true}, Host: requestcontext.Host{Profile: requestcontext.DirectRelayProfile, Env: env, DeviceID: "fixture-resume-device", SessionID: sid, SDKBetas: []string{}, TTLAllowlist: []string{}}}
	prepared, err := resumestate.Prepare(&copy, contextInput, requestcontext.Dependencies{UUID: uuid, Now: now})
	if err != nil {
		return nil, nil, err
	}
	request, err := prepared.BuildAttempt(requestcontext.AttemptOptions{Model: "fixture-model"})
	if err != nil {
		return nil, nil, err
	}
	key := "offline-fixture-key"
	options := provider.ResolveDirectClientOptions(provider.DirectClientInput{Env: env, FallbackAPIKey: &key, SessionID: sid, Runtime: input.Runtime, RequestID: uuid})
	config := options.StreamConfig()
	transport := &http.Transport{}
	defer transport.CloseIdleConnections()
	config.HTTPClient = &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	requests, prefetch := []map[string]any{}, []any{}
	finalize := config.FinalizeHeaders
	config.FinalizeHeaders = func(target string, headers http.Header) (http.Header, error) {
		parsed, err := url.Parse(target)
		if err != nil {
			return nil, err
		}
		if parsed.Scheme+"://"+parsed.Host != input.Origin {
			return nil, errors.New("restored request escaped exact loopback")
		}
		resolved, err := finalize(target, headers)
		if err != nil {
			return nil, err
		}
		rows := [][2]string{}
		for name, values := range resolved {
			rows = append(rows, [2]string{strings.ToLower(name), strings.Join(values, ", ")})
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i][0] < rows[j][0] })
		requests = append(requests, map[string]any{"path": parsed.RequestURI(), "method": "POST", "headers": rows, "body": string(request.PreparedRequest.Body)})
		prefetch = append(prefetch, append([]map[string]any{}, (*allocations)...))
		return resolved, nil
	}
	stream, err := provider.NewAnthropic(config).OpenMessagesStream(context.Background(), request.PreparedRequest)
	if err != nil {
		return nil, nil, err
	}
	for {
		_, err = stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			_ = stream.Close()
			return nil, nil, err
		}
	}
	if err = stream.Close(); err != nil {
		return nil, nil, err
	}
	return map[string]any{"generation": generation, "historyBefore": historyBefore, "requests": requests}, map[string]any{"prefetchAllocations": prefetch}, nil
}
