// Package grokauth ports the CLI's file-backed Grok token authority. The
// caller supplies the same native file and fetch owner used by image tools.
package grokauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/activitystatsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"time"
)

type Options struct {
	Environment    func() map[string]string
	Read           func(context.Context, string) ([]byte, error)
	Save           func(context.Context, string, protocol.JSONObject) (bool, error)
	Refresh        func(context.Context, json.RawMessage, json.RawMessage) (protocol.JSONObject, error)
	NowMillis      func() int64
	Administrative func(error) bool
}
type Service struct {
	options Options
	cache   *fresh
}
type fresh struct {
	authority string
	refresh   json.RawMessage
	tokens    protocol.JSONObject
}

func New(options Options) (*Service, error) {
	if options.Environment == nil || options.Read == nil || options.Save == nil || options.Refresh == nil || options.NowMillis == nil {
		return nil, errors.New("Grok file refresh requires original environment/files/fetch/clock")
	}
	return &Service{options: options}, nil
}
func (s *Service) ClearFreshCache() { s.cache = nil }
func (s *Service) Fresh(ctx context.Context, force bool) (*protocol.JSONObject, error) {
	authority := protocol.TrimJavaScriptString(s.options.Environment()["GROK_OAUTH_FILE"])
	if authority == "" {
		return nil, nil
	}
	data, err := s.options.Read(ctx, authority)
	if err != nil {
		if s.options.Administrative != nil && s.options.Administrative(err) {
			return nil, err
		}
		return nil, nil
	}
	stored := NormalizeFile(data)
	if stored == nil {
		return nil, nil
	}
	if !force {
		if s.cache != nil && s.cache.authority == authority && bytes.Equal(s.cache.refresh, stored.Get("refreshToken")) && !s.expired(s.cache.tokens) {
			value := s.cache.tokens
			return &value, nil
		}
		if !s.expired(*stored) {
			return stored, nil
		}
	}
	response, err := s.options.Refresh(ctx, stored.Get("refreshToken"), stored.Get("clientId"))
	if err != nil {
		return nil, err
	}
	tokens := desktopoauth.WithRefreshedGrokAccessToken(*stored, response, s.options.NowMillis())
	saved, err := s.options.Save(ctx, authority, tokens)
	if err != nil {
		return nil, err
	}
	if !saved {
		return nil, errors.New("Failed to persist refreshed Grok OAuth tokens")
	}
	s.cache = &fresh{authority: authority, refresh: append(json.RawMessage(nil), stored.Get("refreshToken")...), tokens: tokens}
	return &tokens, nil
}
func (s *Service) expired(tokens protocol.JSONObject) bool {
	var expires float64
	_ = json.Unmarshal(tokens.Get("expiresAt"), &expires)
	return expires-float64(s.options.NowMillis()) <= 5*60*1000
}

var grokDates = activitystatsapi.NewIndexDateParser(time.Local)

func NormalizeFile(data []byte) *protocol.JSONObject {
	decoded := bytes.TrimSpace(protocol.DecodeJavaScriptUTF8(data))
	if len(decoded) == 0 || decoded[0] != '{' || !json.Valid(decoded) {
		return nil
	}
	var record protocol.JSONObject
	if record.UnmarshalJSON(decoded) != nil || !record.IsObject() {
		return nil
	}
	stringField := func(keys ...string) json.RawMessage {
		for _, key := range keys {
			raw := record.Get(key)
			if len(raw) > 0 && raw[0] == '"' {
				units, err := protocol.DecodeJavaScriptJSONString(raw)
				if err == nil {
					trimmed := units
					for len(trimmed) > 0 && protocol.IsJavaScriptWhitespace(rune(trimmed[0])) {
						trimmed = trimmed[1:]
					}
					for len(trimmed) > 0 && protocol.IsJavaScriptWhitespace(rune(trimmed[len(trimmed)-1])) {
						trimmed = trimmed[:len(trimmed)-1]
					}
					if len(trimmed) > 0 {
						return protocol.QuoteJavaScriptUTF16(trimmed)
					}
				}
			}
		}
		return nil
	}
	access, refresh := stringField("accessToken", "access_token"), stringField("refreshToken", "refresh_token")
	if access == nil || refresh == nil {
		return nil
	}
	expiry := record.Get("expiresAt")
	if expiry == nil || string(expiry) == "null" {
		expiry = record.Get("expires_at")
	}
	var expires float64
	if len(expiry) > 0 && expiry[0] == '"' {
		var value string
		_ = json.Unmarshal(expiry, &value)
		if protocol.TrimJavaScriptString(value) == "" {
			return nil
		}
		numeric := protocol.TrimJavaScriptString(value)
		parsed, err := strconv.ParseFloat(numeric, 64)
		if len(numeric) > 2 && (strings.HasPrefix(numeric, "0x") || strings.HasPrefix(numeric, "0X") || strings.HasPrefix(numeric, "0o") || strings.HasPrefix(numeric, "0O") || strings.HasPrefix(numeric, "0b") || strings.HasPrefix(numeric, "0B")) {
			n, e := strconv.ParseUint(numeric, 0, 64)
			parsed, err = float64(n), e
		}
		if err == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0) {
			expires = parsed
		} else {
			parsed, ok, err := grokDates.Parse(value)
			if !ok || err != nil {
				return nil
			}
			expires = parsed
		}
	} else if json.Unmarshal(expiry, &expires) != nil || math.IsInf(expires, 0) || math.IsNaN(expires) {
		return nil
	}
	if len(expiry) == 0 || expiry[0] == 't' || expiry[0] == 'f' || string(expiry) == "null" {
		return nil
	}
	result := protocol.NewJSONObject(protocol.JSONField{Name: "accessToken", Value: access}, protocol.JSONField{Name: "refreshToken", Value: refresh}, protocol.JSONField{Name: "expiresAt", Value: json.RawMessage(strconv.FormatFloat(expires, 'f', -1, 64))})
	for _, keys := range [][2]string{{"idToken", "id_token"}, {"email", "email"}, {"clientId", "client_id"}, {"scope", "scope"}, {"tokenType", "token_type"}} {
		if value := stringField(keys[0], keys[1]); value != nil {
			result.Set(keys[0], value)
		}
	}
	return &result
}
