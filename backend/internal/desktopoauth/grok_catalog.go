package desktopoauth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
)

const GrokModelsEndpoint = providerproxy.GrokCLIBaseURL + "/models"
const grokModelCatalogTTL = 5 * time.Minute
const grokModelCatalogBackoff = time.Minute
const grokModelCatalogTimeout = 5 * time.Second

// Source caches one account entry and one process-wide refresh, rather than a
// map of entries or a refresh per account. An account change never reads the
// previous account's models, even while its refresh remains in flight.
type grokModelCache struct {
	mu                    sync.Mutex
	key                   grokModelAccountKey
	models                []providerproxy.GrokModel
	expiresAt, retryAfter int64
	inFlight              bool
}

type grokModelAccountKey struct {
	value     string
	reference *Document
}

// ModelCatalog uses only this desktop account. Token refresh is awaited before
// discovery, exactly as the original Grok desktop model and WS callers. Token-store
// failures propagate before the catalog cache resolves.
func (s *GrokService) ModelCatalog(ctx context.Context, forceRefresh bool) ([]providerproxy.GrokModel, error) {
	ctx, finish, err := s.beginOperation(ctx)
	if err != nil {
		return nil, err
	}
	defer finish()
	tokens, tokenError := s.EnsureFreshTokens(ctx)
	if tokenError != nil {
		return nil, tokenError
	}
	key := grokModelAccountKey{value: "logged-out"}
	if tokens != nil && truthy(tokens.Data) {
		values := tokens.Record()
		value := coalesce(values.Get("email"), encode("authenticated-default"))
		key.value = string(value)
		// Source === compares malformed legacy object/array IDs by identity,
		// not by serialized contents. Each disk load creates a new identity.
		if len(value) > 0 && (value[0] == '{' || value[0] == '[') {
			key.reference = tokens
		}
	}
	fallback := providerproxy.GrokModelCatalog()
	if forceRefresh {
		models, fetchError := s.fetchGrokModelCatalog(ctx, tokens)
		s.catalog.mu.Lock()
		defer s.catalog.mu.Unlock()
		if fetchError != nil {
			s.catalog.retryAfter = s.parent.options.Now().UnixMilli() + grokModelCatalogBackoff.Milliseconds()
			return s.setRuntimeModelCatalog(fallback), nil
		}
		s.storeGrokModelCatalog(key, models)
		return s.setRuntimeModelCatalog(models), nil
	}
	s.catalog.mu.Lock()
	defer s.catalog.mu.Unlock()
	now := s.parent.options.Now().UnixMilli()
	result := fallback
	usable := s.catalog.models != nil && s.catalog.key == key
	if usable {
		result = providerproxy.CloneGrokModels(s.catalog.models)
		if s.catalog.expiresAt > now {
			return s.setRuntimeModelCatalog(result), nil
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
				models, fetchError := s.fetchGrokModelCatalog(refreshContext, tokens)
				s.catalog.mu.Lock()
				defer s.catalog.mu.Unlock()
				if fetchError == nil {
					s.storeGrokModelCatalog(key, models)
				} else {
					s.catalog.retryAfter = s.parent.options.Now().UnixMilli() + grokModelCatalogBackoff.Milliseconds()
				}
				s.catalog.inFlight = false
			}()
		}
	}
	return s.setRuntimeModelCatalog(result), nil
}

func (s *GrokService) storeGrokModelCatalog(key grokModelAccountKey, models []providerproxy.GrokModel) {
	s.catalog.key, s.catalog.models = key, providerproxy.CloneGrokModels(models)
	s.catalog.expiresAt = s.parent.options.Now().UnixMilli() + grokModelCatalogTTL.Milliseconds()
	s.catalog.retryAfter = 0
}

func (s *GrokService) fetchGrokModelCatalog(ctx context.Context, tokens *Document) ([]providerproxy.GrokModel, error) {
	if tokens == nil || !truthy(tokens.Data) {
		return nil, errors.New("Grok OAuth token is unavailable")
	}
	requestContext, cancel := context.WithTimeout(ctx, grokModelCatalogTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, "GET", GrokModelsEndpoint, nil)
	if err != nil {
		return nil, err
	}
	values := tokens.Record()
	if !truthy(values.Get("accessToken")) {
		return nil, errors.New("Grok OAuth token is unavailable")
	}
	headers, err := providerproxy.BuildGrokIdentityHeaders(values.Get("accessToken"), s.parent.options.Platform, runtime.GOARCH)
	if err != nil {
		return nil, err
	}
	request.Header = headers
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
		return nil, fmt.Errorf("Grok models endpoint returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	normalized, err := protocol.ParseAndStringifyJavaScriptJSON(bytes.TrimPrefix(protocol.DecodeJavaScriptUTF8(data), []byte{0xef, 0xbb, 0xbf}))
	if err != nil {
		return nil, err
	}
	models := providerproxy.NormalizeGrokModelCatalog(normalized)
	if len(models) == 0 {
		return nil, errors.New("Grok models endpoint returned no models")
	}
	return s.setRuntimeModelCatalog(models), nil
}

func (s *GrokService) setRuntimeModelCatalog(models []providerproxy.GrokModel) []providerproxy.GrokModel {
	s.runtimeMu.Lock()
	s.runtimeCatalog = providerproxy.CloneGrokModels(models)
	s.runtimeMu.Unlock()
	return providerproxy.CloneGrokModels(models)
}
func (s *GrokService) RuntimeModelCatalog() []providerproxy.GrokModel {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	if s.runtimeCatalog == nil {
		return providerproxy.GrokModelCatalog()
	}
	return providerproxy.CloneGrokModels(s.runtimeCatalog)
}
