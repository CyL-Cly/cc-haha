// Package mcpauth owns native MCP OAuth provider state and explicit consent
// flows. It preserves the original shared credential slots and never starts a
// browser from a connection probe.
package mcpauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	mathrand "math/rand/v2"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/mcpclient"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

const ClientMetadataURL = "https://claude.ai/oauth/claude-code-client-metadata"
const RequestTimeout = 30 * time.Second
const FlowTimeout = 5 * time.Minute

type Options struct {
	Secure                         *securestorage.Store
	Home, ConfigDir, CWD, Platform string
	Environment                    map[string]string
	HTTPClient                     *http.Client
	Now                            func() time.Time
	Random                         io.Reader
	RandomFloat                    func() float64
	Sleep                          func(context.Context, time.Duration) error
	OpenBrowser                    func(context.Context, string) (bool, error)
	OnEvent                        func(string, protocol.JSONObject)
	OnDebug                        func(string, string)
	Settings                       func(context.Context) (protocol.JSONObject, error)
}
type Service struct{ options Options }

func New(o Options) (*Service, error) {
	if o.Secure == nil || !filepath.IsAbs(o.Home) || !filepath.IsAbs(o.ConfigDir) || !filepath.IsAbs(o.CWD) || o.Environment == nil || o.Platform == "" {
		return nil, errors.New("MCP OAuth requires shared secure storage and explicit home, config, cwd, platform and environment")
	}
	if envTruthy(o.Environment["CLAUDE_CODE_ENABLE_XAA"]) && o.Settings == nil {
		return nil, errors.New("XAA requires the actual shared settings producer")
	}
	if o.HTTPClient == nil {
		o.HTTPClient = http.DefaultClient
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Random == nil {
		o.Random = rand.Reader
	}
	if o.RandomFloat == nil {
		o.RandomFloat = mathrand.Float64
	}
	if o.Sleep == nil {
		o.Sleep = func(ctx context.Context, delay time.Duration) error {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		}
	}
	environment := map[string]string{}
	for key, value := range o.Environment {
		environment[key] = value
	}
	o.Environment = environment
	service := &Service{o}
	if service.options.OpenBrowser == nil {
		service.options.OpenBrowser = service.defaultOpenBrowser
	}
	return service, nil
}
func GetServerKey(name string, config protocol.JSONObject) (string, error) {
	headers := config.Get("headers")
	if !truthy(headers) {
		headers = []byte("{}")
	}
	value := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: config.Get("type")}, protocol.JSONField{Name: "url", Value: config.Get("url")}, protocol.JSONField{Name: "headers", Value: headers})
	raw, err := value.MarshalJSON()
	if err != nil {
		return "", err
	}
	raw, err = protocol.ParseAndStringifyJavaScriptJSON(raw)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(raw)
	return name + "|" + hex.EncodeToString(hash[:8]), nil
}
func (s *Service) Provider(_ context.Context, name string, config protocol.JSONObject) (*Provider, error) {
	key, err := GetServerKey(name, config)
	if err != nil {
		return nil, err
	}
	if !config.IsObject() {
		return nil, errors.New("MCP OAuth configuration must be an object")
	}
	return &Provider{service: s, name: name, config: config.Clone(), key: key, redirect: "http://localhost:3118/callback"}, nil
}
func (s *Service) debug(name, message string) {
	if s.options.OnDebug != nil {
		s.options.OnDebug(name, message)
	}
}
func (s *Service) event(name string, fields protocol.JSONObject) {
	if s.options.OnEvent != nil {
		s.options.OnEvent(name, fields)
	}
}
func object(raw json.RawMessage) protocol.JSONObject {
	var o protocol.JSONObject
	_ = o.UnmarshalJSON(raw)
	if !o.IsObject() {
		return protocol.NewJSONObject()
	}
	return o
}
func raw(value any) json.RawMessage { source, _ := json.Marshal(value); return source }
func quote(value string) json.RawMessage {
	units, _ := protocol.DecodeJavaScriptJSONString(raw(value))
	return protocol.QuoteJavaScriptUTF16(units)
}
func record(fields ...protocol.JSONField) protocol.JSONObject {
	return protocol.NewJSONObject(fields...)
}
func field(name string, value any) protocol.JSONField {
	return protocol.JSONField{Name: name, Value: raw(value)}
}
func truthy(raw json.RawMessage) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	switch value := value.(type) {
	case nil:
		return false
	case bool:
		return value
	case float64:
		return value != 0
	case string:
		return value != ""
	}
	return true
}
func (s *Service) change(ctx context.Context, update func(protocol.JSONObject) error) error {
	result, err := s.options.Secure.Change(ctx, update)
	if err != nil {
		return err
	}
	if !result.Success {
		s.debug("", "MCP OAuth credentials could not be persisted")
	}
	// Source secure-storage update is best effort. A failed write never creates
	// a token in memory; subsequent Token reads only the actual shared record.
	return nil
}
func (s *Service) read(ctx context.Context) (protocol.JSONObject, error) {
	value, err := s.options.Secure.Read(ctx)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if value == nil {
		return protocol.NewJSONObject(), nil
	}
	return value.Clone(), nil
}
func (s *Service) entry(ctx context.Context, key string) (protocol.JSONObject, error) {
	value, err := s.read(ctx)
	return object(object(value.Get("mcpOAuth")).Get(key)), err
}
func (s *Service) ClearTokens(ctx context.Context, name string, config protocol.JSONObject) error {
	key, err := GetServerKey(name, config)
	if err != nil {
		return err
	}
	current, err := s.read(ctx)
	if err != nil {
		return err
	}
	if object(current.Get("mcpOAuth")).Get(key) == nil {
		return nil
	}
	return s.change(ctx, func(data protocol.JSONObject) error {
		slots := object(data.Get("mcpOAuth"))
		if slots.Get(key) != nil {
			slots.Delete(key)
			data.Set("mcpOAuth", raw(slots))
		}
		return nil
	})
}
func (s *Service) ClearClientConfig(ctx context.Context, name string, config protocol.JSONObject) error {
	key, err := GetServerKey(name, config)
	if err != nil {
		return err
	}
	current, err := s.read(ctx)
	if err != nil {
		return err
	}
	if object(current.Get("mcpOAuthClientConfig")).Get(key) == nil {
		return nil
	}
	return s.change(ctx, func(data protocol.JSONObject) error {
		slots := object(data.Get("mcpOAuthClientConfig"))
		if slots.Get(key) != nil {
			slots.Delete(key)
			data.Set("mcpOAuthClientConfig", raw(slots))
		}
		return nil
	})
}
func (s *Service) Clear(ctx context.Context, name string, config protocol.JSONObject) error {
	if err := s.ClearTokens(ctx, name, config); err != nil {
		return err
	}
	return s.ClearClientConfig(ctx, name, config)
}
func (s *Service) SaveClientSecret(ctx context.Context, name string, config protocol.JSONObject, secret string) error {
	key, err := GetServerKey(name, config)
	if err != nil {
		return err
	}
	return s.change(ctx, func(data protocol.JSONObject) error {
		slots := object(data.Get("mcpOAuthClientConfig"))
		slots.Set(key, raw(record(field("clientSecret", secret))))
		data.Set("mcpOAuthClientConfig", raw(slots))
		return nil
	})
}
func (s *Service) ClientConfig(ctx context.Context, name string, config protocol.JSONObject) (protocol.JSONObject, error) {
	key, err := GetServerKey(name, config)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	data, err := s.read(ctx)
	return object(object(data.Get("mcpOAuthClientConfig")).Get(key)), err
}
func (s *Service) HasDiscoveryWithoutToken(ctx context.Context, name string, config protocol.JSONObject) (bool, error) {
	if envTruthy(s.options.Environment["CLAUDE_CODE_ENABLE_XAA"]) && truthy(object(config.Get("oauth")).Get("xaa")) {
		return false, nil
	}
	key, err := GetServerKey(name, config)
	if err != nil {
		return false, err
	}
	entry, err := s.entry(ctx, key)
	return entry.Get("discoveryState") != nil && !truthy(entry.Get("accessToken")) && !truthy(entry.Get("refreshToken")), err
}
func (s *Service) ClearDiscoveryWithoutToken(ctx context.Context, name string, config protocol.JSONObject) error {
	key, err := GetServerKey(name, config)
	if err != nil {
		return err
	}
	entry, err := s.entry(ctx, key)
	if err != nil {
		return err
	}
	if !truthy(entry.Get("discoveryState")) || truthy(entry.Get("accessToken")) || truthy(entry.Get("refreshToken")) {
		return nil
	}
	return s.change(ctx, func(data protocol.JSONObject) error {
		slots := object(data.Get("mcpOAuth"))
		entry := object(slots.Get(key))
		if truthy(entry.Get("discoveryState")) && !truthy(entry.Get("accessToken")) && !truthy(entry.Get("refreshToken")) {
			entry.Delete("discoveryState")
			slots.Set(key, raw(entry))
			data.Set("mcpOAuth", raw(slots))
		}
		return nil
	})
}
func envTruthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

type AuthenticationCancelledError struct{}

func (AuthenticationCancelledError) Error() string { return "Authentication was cancelled" }

type OAuthError struct {
	Code, Message, URI string
	Status             int
}

func (e *OAuthError) Error() string { return e.Message }
func (e *OAuthError) retryable() bool {
	return e.Code == "server_error" || e.Code == "temporarily_unavailable" || e.Code == "too_many_requests"
}
func needsAuth(config protocol.JSONObject, challenge mcpclient.AuthChallenge) error {
	return &mcpclient.AuthError{Challenge: challenge}
}
