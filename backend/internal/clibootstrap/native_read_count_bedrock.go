package clibootstrap

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cloudauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/contextusage"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelselection"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Source Bedrock count_tokens is AWS CountTokensCommand, not the Anthropic
// Bedrock Messages wrapper. Its invokeModel body is base64 in the AWS JSON.
func (m *MainPrint) nativeBedrockReadCount(scope *cooperative.Scope, ctx context.Context, model, content string, env map[string]string) (result *float64, failure error) {
	defer func() {
		if cause := context.Cause(ctx); cause != nil {
			result, failure = nil, cause
		}
	}()
	settings, err := m.Startup.GetInitialSettings()
	if err != nil {
		return nil, nil
	}
	selector, err := modelselection.NewParsing(modelselection.Options{Environment: env, Settings: *settings})
	if err != nil {
		return nil, nil
	}
	canonical, err := selector.Canonical(quoteMain(model))
	if err != nil {
		return nil, nil
	}
	var name string
	_ = json.Unmarshal(canonical, &name)
	betas, _ := cloudauth.BedrockBetas(model, name, env, nil, false)
	binding, err := m.nativeBedrockBinding(scope)
	if err != nil || binding == nil {
		return nil, nil
	}
	client, err := binding.Client(ctx, model)
	if err != nil || client.Config == nil {
		return nil, nil
	}
	region := env["AWS_REGION"]
	if region == "" {
		region = env["AWS_DEFAULT_REGION"]
	}
	if region == "" {
		region = "us-east-1"
	}
	base := env["ANTHROPIC_BEDROCK_BASE_URL"]
	if base == "" {
		base = "https://bedrock-runtime." + region + ".amazonaws.com"
	}
	var credentials *cloudauth.AWSCredentials
	if !envTruthyNative(env["CLAUDE_CODE_SKIP_BEDROCK_AUTH"]) && env["AWS_BEARER_TOKEN_BEDROCK"] == "" {
		if client.Config.Credentials == nil {
			return nil, nil
		}
		value, err := client.Config.Credentials(ctx)
		if err != nil {
			return nil, nil
		}
		credentials = &value
	}
	requestOnce := func(method, path string, body json.RawMessage) ([]byte, error) {
		r, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base, "/")+path, strings.NewReader(string(body)))
		if err != nil {
			return nil, err
		}
		r.Header.Set("content-type", "application/json")
		if token := env["AWS_BEARER_TOKEN_BEDROCK"]; token != "" && !envTruthyNative(env["CLAUDE_CODE_SKIP_BEDROCK_AUTH"]) {
			r.Header.Set("authorization", "Bearer "+token)
		}
		if credentials != nil {
			signed := cloudauth.Sign(cloudauth.SigningRequest{Method: method, Path: r.URL.EscapedPath(), Headers: map[string]string{"host": r.URL.Host, "content-type": "application/json"}, Body: body}, *credentials, cloudauth.SignOptions{Region: region, Service: "bedrock", Time: m.options.Native.Now()})
			for key, value := range signed.Headers {
				if key == "host" {
					r.Host = value
				} else {
					r.Header.Set(key, value)
				}
			}
		}
		response, err := m.nativeCloudHTTP(scope, r)
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()
		bytes, err := io.ReadAll(response.Body)
		if err != nil {
			return nil, err
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return nil, &nativeBedrockReadResponseError{response.StatusCode}
		}
		return bytes, nil
	}
	request := func(method, path string, body json.RawMessage) ([]byte, error) {
		attempts := 3
		if value, err := strconv.Atoi(env["AWS_MAX_ATTEMPTS"]); err == nil && value > 0 {
			attempts = value
		}
		for attempt := 0; attempt < attempts; attempt++ {
			bytes, err := requestOnce(method, path, body)
			if err == nil {
				return bytes, nil
			}
			if ctx.Err() != nil {
				return nil, context.Cause(ctx)
			}
			var responseErr *nativeBedrockReadResponseError
			if errors.As(err, &responseErr) && responseErr.status < 500 && responseErr.status != 429 {
				return nil, err
			}
			if attempt+1 == attempts {
				return nil, err
			}
			duration := time.Duration(nativeMathRandom()*math.Pow(2, float64(attempt))*100) * time.Millisecond
			work, waitErr := nativeSkillWork(m, scope, func() (struct{}, error) {
				timer := time.NewTimer(duration)
				defer timer.Stop()
				select {
				case <-timer.C:
					return struct{}{}, nil
				case <-ctx.Done():
					return struct{}{}, context.Cause(ctx)
				}
			})
			if waitErr != nil {
				return nil, waitErr
			}
			if _, waitErr = work.Await(scope); waitErr != nil {
				return nil, waitErr
			}
		}
		return nil, errors.New("Bedrock count attempts exhausted")
	}
	model = contextusage.WireModel(model)
	if !strings.HasPrefix(model, "anthropic.") {
		profileModel, err := m.nativeBedrockCountProfile(scope, model, func() (*string, error) {
			data, err := request(http.MethodGet, "/inference-profiles/"+strings.ReplaceAll(url.QueryEscape(model), "+", "%20"), nil)
			if err != nil {
				return nil, nil
			}
			var profile struct {
				Models []struct {
					ModelARN string `json:"modelArn"`
				}
			}
			if json.Unmarshal(data, &profile) != nil || len(profile.Models) == 0 || profile.Models[0].ModelARN == "" {
				return nil, nil
			}
			value := profile.Models[0].ModelARN
			if at := strings.LastIndex(value, "/"); at >= 0 {
				value = value[at+1:]
			}
			return &value, nil
		})
		if err != nil || profileModel == nil {
			return nil, err
		}
		model = *profileModel
	}
	inner := protocol.NewJSONObject(protocol.JSONField{Name: "anthropic_version", Value: quoteMain("bedrock-2023-05-31")}, protocol.JSONField{Name: "messages", Value: rawMain([]protocol.JSONObject{protocol.NewJSONObject(protocol.JSONField{Name: "role", Value: quoteMain("user")}, protocol.JSONField{Name: "content", Value: quoteMain(content)})})}, protocol.JSONField{Name: "max_tokens", Value: json.RawMessage("1")})
	if len(betas) > 0 {
		inner.Set("anthropic_beta", rawMain(betas))
	}
	data, _ := inner.MarshalJSON()
	data, err = protocol.ParseAndStringifyJavaScriptJSON(data)
	if err != nil {
		return nil, nil
	}
	body := rawMain(map[string]any{"input": map[string]any{"invokeModel": map[string]any{"body": base64.StdEncoding.EncodeToString(data)}}})
	response, err := request(http.MethodPost, "/model/"+strings.ReplaceAll(url.QueryEscape(model), "+", "%20")+"/count-tokens", body)
	if err != nil {
		if ctx.Err() != nil {
			return nil, context.Cause(ctx)
		}
		return nil, nil
	}
	var value protocol.JSONObject
	if value.UnmarshalJSON(response) != nil {
		return nil, nil
	}
	raw := value.Get("inputTokens")
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var n float64
	if json.Unmarshal(raw, &n) != nil {
		var text string
		if json.Unmarshal(raw, &text) != nil {
			return nil, nil
		}
		n, err = strconv.ParseFloat(text, 64)
		if err != nil {
			return nil, nil
		}
	}
	return &n, nil
}

type nativeBedrockReadResponseError struct{ status int }

func (e *nativeBedrockReadResponseError) Error() string { return "Bedrock count request failed" }
