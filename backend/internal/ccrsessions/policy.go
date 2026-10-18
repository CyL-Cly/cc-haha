package ccrsessions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func policyRestrictions(data []byte) (map[string]bool, bool) {
	var body protocol.JSONObject
	if body.UnmarshalJSON(data) != nil {
		return nil, false
	}
	value, ok := body.Object("restrictions")
	if !ok {
		return nil, false
	}
	result := map[string]bool{}
	for _, field := range value.OwnProperties() {
		var item protocol.JSONObject
		if item.UnmarshalJSON(field.Value) != nil || string(item.Get("allowed")) != "true" && string(item.Get("allowed")) != "false" {
			return nil, false
		}
		var name string
		if json.Unmarshal(field.NameJSON, &name) != nil {
			return nil, false
		}
		result[name] = string(item.Get("allowed")) == "true"
	}
	return result, true
}
func policyBody(restrictions map[string]bool) protocol.JSONObject {
	keys := []string{}
	for key := range restrictions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	value := protocol.NewJSONObject()
	for _, key := range keys {
		allowed := []byte("false")
		if restrictions[key] {
			allowed = []byte("true")
		}
		item := protocol.NewJSONObject(protocol.JSONField{Name: "allowed", Value: allowed})
		data, _ := item.MarshalJSON()
		value.Set(key, data)
	}
	return value
}
func (s *Service) policyEligible(credentials Credentials) bool {
	env := s.options.Environment
	if env["CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH"] == "1" || truthy(env["CLAUDE_CODE_USE_BEDROCK"]) || truthy(env["CLAUDE_CODE_USE_VERTEX"]) || truthy(env["CLAUDE_CODE_USE_FOUNDRY"]) || truthy(env["CLAUDE_CODE_USE_AZURE_OPENAI"]) {
		return false
	}
	if base := env["ANTHROPIC_BASE_URL"]; base != "" {
		u, err := url.Parse(base)
		if err != nil || !strings.EqualFold(u.Hostname(), "api.anthropic.com") {
			return false
		}
	}
	if credentials.APIKey != "" {
		return true
	}
	if credentials.Token == "" || credentials.Subscription != "team" && credentials.Subscription != "enterprise" {
		return false
	}
	for _, scope := range credentials.Scopes {
		if scope == "user:inference" {
			return true
		}
	}
	return false
}

// PolicyRemoteAllowed reuses Source's old policy-limits.json fixture shape.
// A transient fetch keeps its stale restrictions; ineligible third-party
// providers never consume a cache from a different first-party account.
func (s *Service) PolicyRemoteAllowed(ctx context.Context) (bool, error) {
	ctx, done, err := s.operation(ctx)
	if err != nil {
		return false, err
	}
	defer done()
	s.policyOnce.Do(func() {
		allowed, err := s.loadPolicyRemoteAllowed(ctx)
		s.policyMu.Lock()
		s.policyAllowed, s.policyError = allowed, err
		active := s.policyActive
		s.policyMu.Unlock()
		if err == nil && active {
			s.startPolicyPolling()
		}
	})
	if ctx.Err() != nil {
		return false, context.Cause(ctx)
	}
	s.policyMu.RLock()
	defer s.policyMu.RUnlock()
	return s.policyAllowed, s.policyError
}

func (s *Service) startPolicyPolling() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.workers.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.workers.Done()
		interval := s.options.PolicyPollInterval
		if interval <= 0 {
			interval = time.Hour
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-s.life.Done():
				return
			case <-ticker.C:
				allowed, err := s.loadPolicyRemoteAllowed(s.life)
				if s.life.Err() != nil {
					return
				}
				s.policyMu.Lock()
				s.policyAllowed, s.policyError = allowed, err
				s.policyMu.Unlock()
			}
		}
	}()
}

func (s *Service) loadPolicyRemoteAllowed(ctx context.Context) (bool, error) {
	credentials, err := s.options.Credentials(ctx, false)
	if ctx.Err() != nil {
		return false, context.Cause(ctx)
	}
	if err != nil || !s.policyEligible(credentials) {
		return true, nil
	}
	s.policyMu.Lock()
	s.policyActive = true
	s.policyMu.Unlock()
	path := filepath.Join(s.options.ConfigDir, "policy-limits.json")
	cached, valid := map[string]bool(nil), false
	if data, err := os.ReadFile(path); err == nil {
		cached, valid = policyRestrictions(data)
	}
	allowed := func(restrictions map[string]bool) bool {
		value, exists := restrictions["allow_remote_sessions"]
		return !exists || value
	}
	extra := http.Header{}
	if credentials.APIKey != "" {
		extra.Set("x-api-key", credentials.APIKey)
		extra["Authorization"] = nil
	} else {
		extra.Set("anthropic-beta", "oauth-2025-04-20")
	}
	extra.Set("User-Agent", "claude-code/"+s.options.Environment["CC_HAHA_VERSION"])
	if valid {
		data, _ := policyBody(cached).MarshalJSON()
		digest := sha256.Sum256(data)
		extra.Set("If-None-Match", `"sha256:`+hex.EncodeToString(digest[:])+`"`)
	}
	for attempt := 0; attempt < 6; attempt++ {
		body, status, failure := s.request(ctx, "GET", "/api/claude_code/policy_limits", nil, credentials, false, 10*time.Second, extra)
		if ctx.Err() != nil {
			return false, context.Cause(ctx)
		}
		if failure == nil && status == 304 && valid {
			return allowed(cached), nil
		}
		if failure == nil && status == 404 {
			_ = os.Remove(path)
			return true, nil
		}
		if failure == nil && status == 200 {
			data, _ := body.MarshalJSON()
			if restrictions, ok := policyRestrictions(data); ok {
				if len(restrictions) == 0 {
					_ = os.Remove(path)
				} else if os.MkdirAll(s.options.ConfigDir, 0700) == nil {
					persisted, _ := json.MarshalIndent(body, "", "  ")
					_ = os.WriteFile(path, persisted, 0600)
				}
				return allowed(restrictions), nil
			}
		}
		if status == 401 || status == 403 || attempt == 5 {
			break
		}
		if err := s.options.Wait(ctx, time.Second*time.Duration(1<<attempt)); err != nil {
			return false, err
		}
	}
	return allowed(cached), nil
}
