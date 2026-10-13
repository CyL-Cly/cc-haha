package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/requestmapping"
)

type directClientFixtureStep struct {
	Name              string
	Env               map[string]string
	ExplicitAPIKey    *string
	Workload          string
	Model             string
	BeforeFetchEnv    map[string]string
	ObserveRequestIDs bool
	ObserveFetch      *bool
	Headers           *struct {
		Kind           string
		Entries        [][2]json.RawMessage
		UndefinedNames []string
	}
}

type directClientFixtureInput struct {
	HomeDir   string
	Runtime   SourceRuntime
	Steps     []directClientFixtureStep
	Resolvers []clientResolverFixture
}

type clientResolverFixture struct {
	Name       string
	Env        map[string]string
	RequestURL string
}

func observeClientResolvers(input []clientResolverFixture) []any {
	results := []any{}
	for _, resolver := range input {
		results = append(results, map[string]any{"name": resolver.Name, "managedToken": ResolveManagedProviderProxyAccessToken(resolver.Env, resolver.RequestURL), "injectRequestID": injectFirstPartyRequestID(resolver.Env)})
	}
	return results
}

func TestDirectClientOptionsFixture(t *testing.T) {
	if os.Getenv("CC_HAHA_CLIENT_OPTIONS_FIXTURE") != "1" {
		t.Skip("private direct client parity fixture only")
	}
	var input directClientFixtureInput
	raw, err := io.ReadAll(os.Stdin)
	if err != nil || json.Unmarshal(raw, &input) != nil || input.HomeDir == "" || input.HomeDir != os.Getenv("HOME") || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(input.HomeDir, ".claude") {
		t.Fatal("Client options fixture requires disposable HOME/config")
	}
	output, err := json.Marshal(map[string]any{"results": observeDirectClientOptions(t, input), "resolverResults": observeClientResolvers(input.Resolvers)})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("CLIENT_OPTIONS_RESULT:%s\n", output)
}

func observeDirectClientOptions(t *testing.T, input directClientFixtureInput) []any {
	t.Helper()
	results := []any{}
	for _, step := range input.Steps {
		uuidCalls := 0
		finalEnv := copyClientEnvironment(step.Env)
		for name, value := range step.BeforeFetchEnv {
			finalEnv[name] = value
		}
		var fallback *string
		if key := step.Env["ANTHROPIC_API_KEY"]; key != "" {
			fallback = &key
		}
		profile := ResolveDirectClientOptions(DirectClientInput{
			Env: step.Env, ExplicitAPIKey: step.ExplicitAPIKey, FallbackAPIKey: fallback,
			SessionID: "00000000-0000-4000-8000-000000000000", Workload: step.Workload,
			Runtime:          SourceRuntime{Platform: input.Runtime.Platform, Arch: input.Runtime.Arch, Version: input.Runtime.Version},
			FetchEnvironment: func() map[string]string { return finalEnv },
			RequestID:        func() string { uuidCalls++; return fmt.Sprintf("request-fixture-%d", uuidCalls) },
		})
		config := profile.StreamConfig()
		var observation, failure any
		config.HTTPClient = &http.Client{Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(request.Body)
			if err != nil {
				return nil, err
			}
			names := []string{}
			for name := range request.Header {
				names = append(names, strings.ToLower(name))
			}
			sort.Strings(names)
			headers := [][2]string{}
			for _, name := range names {
				headers = append(headers, [2]string{name, strings.Join(request.Header.Values(name), ", ")})
			}
			observation = map[string]any{"url": request.URL.String(), "method": request.Method, "headers": headers, "body": string(body)}
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"message_start\",\"message\":{\"id\":\"fixture\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":0,\"output_tokens\":0}}}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n"))}, nil
		})}
		if step.ObserveFetch == nil || *step.ObserveFetch {
			model := step.Model
			if model == "" {
				model = "fixture-model"
			}
			modelJSON, _ := json.Marshal(model)
			params, err := requestmapping.ParseValue([]byte(`{"model":` + string(modelJSON) + `,"messages":[],"max_tokens":32,"stream":true}`))
			if err != nil {
				t.Fatal(err)
			}
			options := BetaMessageOptions{}
			if step.Headers != nil {
				if step.Headers.Kind != "object" {
					t.Fatal("Client options fixture supports only object request headers")
				}
				options.HeaderKind = step.Headers.Kind
				for _, pair := range step.Headers.Entries {
					var entry HeaderEntry
					_ = json.Unmarshal(pair[0], &entry.Name)
					if len(pair[1]) > 0 && pair[1][0] == '[' {
						_ = json.Unmarshal(pair[1], &entry.Values)
					} else {
						var value *string
						_ = json.Unmarshal(pair[1], &value)
						entry.Values = []*string{value}
					}
					options.Headers = append(options.Headers, entry)
				}
				for _, name := range step.Headers.UndefinedNames {
					options.Headers = append(options.Headers, HeaderEntry{Name: name})
				}
			}
			prepared, err := PrepareBetaMessages(params, options)
			if err == nil {
				_, err = NewAnthropic(config).StreamMessage(context.Background(), prepared, nil)
			}
			if err != nil {
				name := "Error"
				var headerFailure *BunHeaderError
				if errors.As(err, &headerFailure) {
					name = "TypeError"
				}
				failure = map[string]string{"name": name, "message": err.Error()}
			}
		}
		var timeout any = profile.TimeoutMillis
		if math.IsNaN(profile.TimeoutMillis) || math.IsInf(profile.TimeoutMillis, 0) {
			timeout = nil
		}
		result := map[string]any{"name": step.Name, "apiKey": profile.APIKey, "authToken": profile.AuthToken, "baseURL": profile.BaseURL, "timeout": timeout, "defaultHeaders": profile.DefaultHeaders, "fetch": observation, "error": failure}
		if step.ObserveRequestIDs {
			result["uuidCalls"] = uuidCalls
		}
		results = append(results, result)
	}
	return results
}
