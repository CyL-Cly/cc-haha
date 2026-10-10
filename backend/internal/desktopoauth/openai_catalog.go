package desktopoauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
)

const OpenAICodexModelsEndpoint = "https://chatgpt.com/backend-api/codex/models?client_version=0.157.0"
const openAIModelCatalogTTL = 5 * time.Minute
const openAIModelCatalogBackoff = time.Minute
const openAIModelCatalogTimeout = 5 * time.Second

// Source caches one account entry and one process-wide refresh, rather than a
// map of entries or a refresh per account. An account change never reads the
// previous account's models, even while its refresh remains in flight.
type openAIModelCache struct {
	mu                    sync.Mutex
	key                   openAIModelAccountKey
	models                []providerproxy.OpenAIModel
	expiresAt, retryAfter int64
	inFlight              bool
}

type openAIModelAccountKey struct {
	value     string
	reference *Document
}

func cloneOpenAIModels(models []providerproxy.OpenAIModel) []providerproxy.OpenAIModel {
	copy := append([]providerproxy.OpenAIModel{}, models...)
	for i := range copy {
		copy[i].Raw = append([]byte{}, models[i].Raw...)
		copy[i].SupportedReasoningEfforts = append([]string{}, models[i].SupportedReasoningEfforts...)
	}
	return copy
}

// ModelCatalog uses only this desktop account. Token refresh is awaited before
// discovery, exactly as the original desktop wrapper; failures become explicit
// null credentials and can never fall through to the CLI credential slot.
func (s *OpenAIService) ModelCatalog(ctx context.Context, forceRefresh bool) ([]providerproxy.OpenAIModel, error) {
	ctx, finish, err := s.beginOperation(ctx)
	if err != nil {
		return nil, err
	}
	defer finish()
	tokens, tokenError := s.EnsureFreshTokens(ctx)
	if tokenError != nil {
		tokens = nil
	}
	key := openAIModelAccountKey{value: "logged-out"}
	if tokens != nil && truthy(tokens.Data) {
		values := tokens.Record()
		value := coalesce(values.Get("accountId"), values.Get("email"), encode("authenticated-default"))
		key.value = string(value)
		// Source === compares malformed legacy object/array IDs by identity,
		// not by serialized contents. Each disk load creates a new identity.
		if len(value) > 0 && (value[0] == '{' || value[0] == '[') {
			key.reference = tokens
		}
	}
	fallback := providerproxy.OpenAIModelCatalog()
	if forceRefresh {
		models, fetchError := s.fetchOpenAIModelCatalog(ctx, tokens)
		s.catalog.mu.Lock()
		defer s.catalog.mu.Unlock()
		if fetchError != nil {
			s.catalog.retryAfter = s.parent.options.Now().UnixMilli() + openAIModelCatalogBackoff.Milliseconds()
			return fallback, nil
		}
		s.storeOpenAIModelCatalog(key, models)
		return cloneOpenAIModels(models), nil
	}
	s.catalog.mu.Lock()
	defer s.catalog.mu.Unlock()
	now := s.parent.options.Now().UnixMilli()
	result := fallback
	usable := s.catalog.models != nil && s.catalog.key == key
	if usable {
		result = cloneOpenAIModels(s.catalog.models)
		if s.catalog.expiresAt > now {
			return result, nil
		}
	}
	if now >= s.catalog.retryAfter && !s.catalog.inFlight {
		// Reserve the operation before starting the goroutine, so Close cannot
		// race Add/Wait and cannot return while the refresh owns transport work.
		refreshContext, done, startError := s.beginOperation(s.ctx)
		if startError == nil {
			s.catalog.inFlight = true
			go func() {
				defer done()
				models, fetchError := s.fetchOpenAIModelCatalog(refreshContext, tokens)
				s.catalog.mu.Lock()
				defer s.catalog.mu.Unlock()
				if fetchError == nil {
					s.storeOpenAIModelCatalog(key, models)
				} else {
					s.catalog.retryAfter = s.parent.options.Now().UnixMilli() + openAIModelCatalogBackoff.Milliseconds()
				}
				s.catalog.inFlight = false
			}()
		}
	}
	return result, nil
}

func (s *OpenAIService) storeOpenAIModelCatalog(key openAIModelAccountKey, models []providerproxy.OpenAIModel) {
	s.catalog.key, s.catalog.models = key, cloneOpenAIModels(models)
	s.catalog.expiresAt = s.parent.options.Now().UnixMilli() + openAIModelCatalogTTL.Milliseconds()
	s.catalog.retryAfter = 0
}

func (s *OpenAIService) fetchOpenAIModelCatalog(ctx context.Context, tokens *Document) ([]providerproxy.OpenAIModel, error) {
	if tokens == nil || !truthy(tokens.Data) {
		return nil, errors.New("OpenAI OAuth token is unavailable")
	}
	requestContext, cancel := context.WithTimeout(ctx, openAIModelCatalogTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, "GET", OpenAICodexModelsEndpoint, nil)
	if err != nil {
		return nil, err
	}
	values := tokens.Record()
	access, err := protocol.ExactJavaScriptStringJSON(values.Get("accessToken"))
	if err != nil {
		return nil, err
	}
	accessString, err := openAICatalogHeaderValue(access, "Bearer ")
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", accessString)
	request.Header.Set("originator", "codex_cli_rs")
	request.Header.Set("User-Agent", OpenAICodexTokenUserAgent)
	if truthy(values.Get("accountId")) {
		account, e := protocol.ExactJavaScriptStringJSON(values.Get("accountId"))
		if e != nil {
			return nil, e
		}
		accountString, e := openAICatalogHeaderValue(account, "")
		if e != nil {
			return nil, e
		}
		request.Header.Set("ChatGPT-Account-Id", accountString)
	}
	// The original model endpoint uses global fetch, unlike token exchange's
	// getProxyFetchOptions. Preserve that boundary and the explicit HTTP client.
	configuredClient, err := s.parent.openAIModelHTTPClient()
	if err != nil {
		return nil, err
	}
	client := *configuredClient
	client.Timeout = 0
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("OpenAI models endpoint returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	normalized, err := protocol.ParseAndStringifyJavaScriptJSON(bytes.TrimPrefix(protocol.DecodeJavaScriptUTF8(data), []byte{0xef, 0xbb, 0xbf}))
	if err != nil {
		return nil, err
	}
	var body protocol.JSONObject
	if err = body.UnmarshalJSON(normalized); err != nil {
		return nil, err
	}
	var rows []json.RawMessage
	if json.Unmarshal(body.Get("models"), &rows) != nil || string(body.Get("models")) == "null" {
		return nil, errors.New("OpenAI models endpoint returned an invalid response")
	}
	models := []providerproxy.OpenAIModel{}
	for _, row := range rows {
		model, err := normalizeRemoteOpenAIModel(row)
		if err != nil {
			return nil, err
		}
		if model != nil {
			models = append(models, *model)
		}
	}
	if len(models) == 0 {
		return nil, errors.New("OpenAI models endpoint returned no visible models")
	}
	return models, nil
}

func openAICatalogHeaderValue(raw json.RawMessage, prefix string) (string, error) {
	units, err := protocol.DecodeJavaScriptJSONString(raw)
	if err != nil {
		return "", err
	}
	for i, unit := range units {
		if unit > 255 {
			return "", fmt.Errorf("Cannot convert argument to a ByteString because the character at index %d has a value of %d which is greater than 255", i+len(prefix), unit)
		}
	}
	var text string
	_ = json.Unmarshal(raw, &text)
	text = strings.Trim(prefix+text, "\t \r\n")
	if strings.ContainsAny(text, "\x00\r\n") {
		return "", errors.New("Headers.set: invalid header value")
	}
	return text, nil
}

func normalizeRemoteOpenAIModel(raw json.RawMessage) (*providerproxy.OpenAIModel, error) {
	if string(raw) == "null" {
		return nil, errors.New("null is not an object (evaluating 'model.slug')")
	}
	row := object(raw)
	slugRaw := row.Get("slug")
	var slug string
	if json.Unmarshal(slugRaw, &slug) != nil || protocol.TrimJavaScriptString(slug) == "" || row.String("visibility") != "list" {
		return nil, nil
	}
	fallback := providerproxy.OpenAIModelCatalogEntry(slug, providerproxy.OpenAIModelCatalog())
	result := record()
	result.Set("value", slugRaw)
	label := row.Get("display_name")
	var labelText string
	if json.Unmarshal(label, &labelText) != nil || protocol.TrimJavaScriptString(labelText) == "" {
		label = slugRaw
		if fallback != nil {
			label = encode(fallback.Label)
		}
	}
	result.Set("label", label)
	description := row.Get("description")
	units, err := protocol.DecodeJavaScriptJSONString(description)
	if err != nil {
		description = encode("")
		if fallback != nil {
			description = encode(fallback.Description)
		}
	} else {
		// Original /\.$/ has no multiline flag: only a final dot is removed.
		index := len(units) - 1
		if index >= 0 && units[index] == '.' {
			units = append(units[:index], units[index+1:]...)
		}
		description = protocol.QuoteJavaScriptUTF16(units)
	}
	result.Set("description", description)
	efforts := []string{}
	var levels []json.RawMessage
	_ = json.Unmarshal(row.Get("supported_reasoning_levels"), &levels)
	for _, level := range levels {
		value := object(level).Get("effort")
		if providerproxy.IsOpenAIReasoningEffort(value) {
			var effort string
			_ = json.Unmarshal(value, &effort)
			efforts = append(efforts, effort)
		}
	}
	defaultEffort := "medium"
	if providerproxy.IsOpenAIReasoningEffort(row.Get("default_reasoning_level")) {
		defaultEffort = row.String("default_reasoning_level")
	} else if fallback != nil {
		defaultEffort = fallback.DefaultReasoningEffort
	} else if len(efforts) > 0 {
		defaultEffort = efforts[0]
	}
	result.Set("defaultReasoningEffort", encode(defaultEffort))
	if len(efforts) == 0 {
		efforts = []string{"low", "medium", "high"}
		if fallback != nil {
			efforts = fallback.SupportedReasoningEfforts
		}
	}
	result.Set("supportedReasoningEfforts", encode(efforts))
	contextRaw := row.Get("context_window")
	var window float64
	if len(contextRaw) > 0 && contextRaw[0] != '"' && string(contextRaw) != "null" && json.Unmarshal(contextRaw, &window) == nil && !math.IsInf(window, 0) && !math.IsNaN(window) {
		percent := 95.0
		percentRaw := row.Get("effective_context_window_percent")
		if len(percentRaw) > 0 && percentRaw[0] != '"' && string(percentRaw) != "null" {
			var n float64
			if json.Unmarshal(percentRaw, &n) == nil {
				percent = n
			}
		}
		window = math.Floor(window * percent / 100)
		if window != 0 && !math.IsNaN(window) {
			result.Set("contextWindow", numberJSON(window))
		}
	} else if fallback != nil && fallback.ContextWindow != 0 {
		result.Set("contextWindow", encode(fallback.ContextWindow))
	}
	var model providerproxy.OpenAIModel
	if err = json.Unmarshal(encode(result), &model); err != nil {
		return nil, err
	}
	return &model, nil
}
