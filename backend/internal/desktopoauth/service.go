// Package desktopoauth owns the original desktop-managed Claude account,
// separate from the CLI credential slot. All state paths and I/O are explicit.
package desktopoauth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliglobalconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

type Options struct {
	Home, ConfigDir, Platform string
	Environment               func() map[string]string
	Store                     *storage.Store
	Secure                    *securestorage.Store
	Global                    *cliglobalconfig.Service
	RunSecurity               func(context.Context, securestorage.Command) ([]byte, error)
	HTTPClient                *http.Client
	Settings                  func(context.Context) (protocol.JSONObject, error)
	ProxyURL                  func(context.Context) (*string, error)
	Now                       func() time.Time
	Random                    io.Reader
	PID                       int
	OnRefreshFailure          func(expected bool, error error)
	// Explicit production transport/fixture seam; nil uses resolved official
	// endpoints. It cannot silently redirect credentials via environment.
	Config            func() (Config, error)
	FetchProfile      func(context.Context, string) (protocol.JSONObject, error)
	FetchAccountUUID  func(context.Context, string) (string, error)
	AccountLookupWait time.Duration // Private clock-bound fixtures; production uses 3 seconds.
	Refresh           func(context.Context, string, json.RawMessage) (protocol.JSONObject, error)
	LoadCLITokens     func(context.Context) (*protocol.JSONObject, error)
	ReadTokenFD       func(context.Context) (string, error)
	// Explicit paths allow the original FD/CCR reader to run in disposable
	// fixtures. Production defaults are the original OS descriptor and CCR path.
	TokenFDPath       func(int64) string
	CCROAuthTokenPath string
	APIKeyFDPath      func(int64) string
	CCRAPIKeyPath     string
}
type Session struct {
	State        string  `json:"state"`
	CodeVerifier string  `json:"codeVerifier"`
	AuthorizeURL string  `json:"authorizeUrl"`
	ServerPort   float64 `json:"serverPort"`
	CreatedAt    int64   `json:"createdAt"`
}
type Document struct{ Data json.RawMessage }

func (d *Document) Record() protocol.JSONObject {
	if d == nil {
		return record()
	}
	return object(d.Data)
}

type pendingRefresh struct {
	done  chan struct{}
	value *Document
}
type Service struct {
	mu                       sync.Mutex
	options                  Options
	sessions                 map[string]Session
	refreshes                map[string]*pendingRefresh
	ownedGlobal              bool
	cliMu                    sync.Mutex
	cliCached                bool
	cliTokens                *protocol.JSONObject
	fdOnce                   sync.Once
	fdToken                  string
	metadataAPIKeyOnce       sync.Once
	metadataAPIKey           string
	metadataLegacyAPIKeyOnce sync.Once
	metadataLegacyAPIKey     string
	metadataOpenAIMu         sync.Mutex
	metadataOpenAICache      map[string]*protocol.JSONObject
	openAI                   *OpenAIService
	grok                     *GrokService
	catalogHTTPExplicit      bool
	catalogHTTPOnce          sync.Once
	catalogHTTP              *http.Client
	catalogHTTPError         error
	accountUUID              string
	accountRetryAt           int64
	accountGeneration        uint64
	accountLookup            *accountUUIDLookup
	accountLife              context.Context
	accountCancel            context.CancelFunc
	accountWorkers           sync.WaitGroup
	accountClosed            bool
}

func New(o Options) (*Service, error) {
	if !filepath.IsAbs(o.Home) || !filepath.IsAbs(o.ConfigDir) || o.Environment == nil {
		return nil, errors.New("Desktop OAuth requires explicit home, config and process environment")
	}
	if o.Platform == "" {
		o.Platform = runtime.GOOS
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Random == nil {
		o.Random = rand.Reader
	}
	if o.PID == 0 {
		o.PID = os.Getpid()
	}
	if o.Store == nil {
		o.Store = storage.New(storage.Options{})
	}
	catalogHTTPExplicit := o.HTTPClient != nil
	if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{}
	}
	if o.Secure == nil {
		var err error
		env := o.Environment()
		o.Secure, err = securestorage.New(securestorage.Options{Home: o.Home, ConfigDir: o.ConfigDir, Platform: o.Platform, Environment: env, ConfigDirExplicit: env["CLAUDE_CONFIG_DIR"] != "", Now: o.Now, RunSecurity: o.RunSecurity})
		if err != nil {
			return nil, err
		}
	}
	s := &Service{options: o, sessions: map[string]Session{}, refreshes: map[string]*pendingRefresh{}, catalogHTTPExplicit: catalogHTTPExplicit}
	if o.Global == nil {
		env := o.Environment()
		var config *string
		if value, ok := env["CLAUDE_CONFIG_DIR"]; ok {
			config = &value
		}
		suffix := ""
		if env["CLAUDE_CODE_CUSTOM_OAUTH_URL"] != "" {
			suffix = "-custom-oauth"
		} else if env["USER_TYPE"] == "ant" {
			if envTruthy(env["USE_LOCAL_OAUTH"]) {
				suffix = "-local-oauth"
			} else if envTruthy(env["USE_STAGING_OAUTH"]) {
				suffix = "-staging-oauth"
			}
		}
		global, err := cliglobalconfig.New(cliglobalconfig.Options{Home: o.Home, ConfigDir: config, OAuthFileSuffix: suffix, Now: o.Now, NowMillis: func() float64 { return float64(o.Now().UnixMilli()) }, PID: o.PID})
		if err != nil {
			return nil, err
		}
		if err = global.Enable(); err != nil {
			return nil, err
		}
		s.options.Global = global
		s.ownedGlobal = true
	}
	if o.Config == nil {
		s.options.Config = func() (Config, error) { return ResolveConfig(o.Environment()) }
	}
	if o.FetchProfile == nil {
		s.options.FetchProfile = s.fetchProfile
	}
	if o.Refresh == nil {
		s.options.Refresh = s.refresh
	}
	if o.LoadCLITokens == nil {
		s.options.LoadCLITokens = s.loadCLITokens
	}
	if o.ReadTokenFD == nil {
		s.options.ReadTokenFD = s.readOAuthTokenFD
	}
	s.openAI = NewOpenAIService(s, nil)
	s.grok = NewGrokService(s, nil)
	return s, nil
}
func (s *Service) OpenAI() *OpenAIService { return s.openAI }
func (s *Service) Grok() *GrokService     { return s.grok }
func (s *Service) Close() error {
	s.mu.Lock()
	s.accountClosed = true
	if s.accountCancel != nil {
		s.accountCancel()
	}
	s.mu.Unlock()
	s.accountWorkers.Wait()
	if s.grok != nil {
		s.grok.Close()
	}
	if s.openAI != nil {
		s.openAI.Close()
	}
	if s.ownedGlobal {
		return s.options.Global.Close()
	}
	return nil
}
func (s *Service) TokenPath() string {
	return filepath.Join(s.options.ConfigDir, "cc-haha", "oauth.json")
}
func (s *Service) LoadTokens(ctx context.Context) (*Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := s.options.Store.FileSystem().ReadFile(s.TokenPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	data = protocol.DecodeJavaScriptUTF8(data)
	normalized, err := protocol.ParseAndStringifyJavaScriptJSON(data)
	if err != nil {
		return nil, err
	}
	if string(normalized) == "null" {
		return nil, nil
	}
	return &Document{normalized}, nil
}
func (s *Service) SaveTokens(ctx context.Context, value protocol.JSONObject) error {
	data, err := value.MarshalJSON()
	if err != nil {
		return err
	}
	return s.saveDocument(ctx, &Document{data})
}
func (s *Service) saveDocument(ctx context.Context, value *Document) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	normalized, err := protocol.ParseAndStringifyJavaScriptJSON(value.Data)
	if err != nil {
		return err
	}
	var formatted bytes.Buffer
	if err = json.Indent(&formatted, normalized, "", "  "); err != nil {
		return err
	}
	fs := s.options.Store.FileSystem()
	path := s.TokenPath()
	if err = fs.MkdirAll(filepath.Dir(path), 0777); err != nil {
		return err
	}
	tmp := path + ".tmp." + strconv.Itoa(s.options.PID)
	if err = fs.WriteFile(tmp, formatted.Bytes(), 0600); err != nil {
		return err
	}
	return fs.Rename(tmp, path)
}
func (s *Service) DeleteTokens(ctx context.Context) error {
	s.forgetAccountUUID()
	if err := ctx.Err(); err != nil {
		return err
	}
	err := s.options.Store.FileSystem().Remove(s.TokenPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
func (s *Service) StartSession(port float64) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.options.Now().UnixMilli()
	for state, value := range s.sessions {
		if now-value.CreatedAt > 5*60*1000 {
			delete(s.sessions, state)
		}
	}
	verifierBytes, stateBytes := make([]byte, 32), make([]byte, 32)
	if _, err := io.ReadFull(s.options.Random, verifierBytes); err != nil {
		return Session{}, err
	}
	verifier := base64.RawURLEncoding.EncodeToString(verifierBytes)
	challenge := sha256.Sum256([]byte(verifier))
	if _, err := io.ReadFull(s.options.Random, stateBytes); err != nil {
		return Session{}, err
	}
	state := base64.RawURLEncoding.EncodeToString(stateBytes)
	c, err := s.options.Config()
	if err != nil {
		return Session{}, err
	}
	address, err := BuildAuthURL(c, base64.RawURLEncoding.EncodeToString(challenge[:]), state, port)
	if err != nil {
		return Session{}, err
	}
	session := Session{state, verifier, address, port, s.options.Now().UnixMilli()}
	s.sessions[state] = session
	return session, nil
}
func (s *Service) session(state string, consume bool) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.sessions[state]
	if !ok {
		return nil
	}
	if s.options.Now().UnixMilli()-value.CreatedAt > 5*60*1000 {
		delete(s.sessions, state)
		return nil
	}
	if consume {
		delete(s.sessions, state)
	}
	return &value
}
func (s *Service) GetSession(state string) *Session     { return s.session(state, false) }
func (s *Service) ConsumeSession(state string) *Session { return s.session(state, true) }
func (s *Service) CompleteSession(ctx context.Context, code, state string) (*Document, error) {
	session := s.ConsumeSession(state)
	if session == nil {
		return nil, errors.New("OAuth session not found or expired")
	}
	c, err := s.options.Config()
	if err != nil {
		return nil, err
	}
	body := record(field("grant_type", "authorization_code"), field("code", code), field("redirect_uri", "http://localhost:"+jsNumber(session.ServerPort)+"/callback"), field("client_id", c.ClientID), field("code_verifier", session.CodeVerifier), field("state", state))
	response, err := s.requestJSON(ctx, "POST", c.TokenURL, body, "", 15*time.Second, true)
	if err != nil {
		return nil, err
	}
	profile, err := s.options.FetchProfile(ctx, response.String("access_token"))
	if err != nil {
		return nil, err
	}
	expires := float64(s.options.Now().UnixMilli()) + number(response.Get("expires_in"))*1000
	tokens := record(protocol.JSONField{Name: "accessToken", Value: response.Get("access_token")}, protocol.JSONField{Name: "refreshToken", Value: coalesce(response.Get("refresh_token"), []byte("null"))}, protocol.JSONField{Name: "expiresAt", Value: numberJSON(expires)}, field("scopes", ParseScopes(response.String("scope"))), protocol.JSONField{Name: "subscriptionType", Value: profile.Get("subscriptionType")})
	if err = s.SaveTokens(ctx, tokens); err != nil {
		return nil, err
	}
	return &Document{encode(tokens)}, nil
}
func (s *Service) EnsureFreshTokens(ctx context.Context) (*Document, error) {
	tokens, err := s.LoadTokens(ctx)
	if err != nil || tokens == nil || !truthy(tokens.Data) {
		if tokens != nil && !truthy(tokens.Data) {
			tokens = nil
		}
		return tokens, err
	}
	record := tokens.Record()
	if string(record.Get("expiresAt")) == "null" || !(float64(s.options.Now().UnixMilli()+5*60*1000) >= number(record.Get("expiresAt"))) {
		return tokens, nil
	}
	if !truthy(record.Get("refreshToken")) {
		return nil, nil
	}
	return s.refreshOnce(ctx, tokens)
}
func (s *Service) RecoverFromUnauthorized(ctx context.Context, failed string) (*Document, error) {
	rejected := strings.TrimSpace(failed)
	if rejected == "" {
		return nil, nil
	}
	tokens, err := s.LoadTokens(ctx)
	if err != nil || tokens == nil {
		return tokens, err
	}
	r := tokens.Record()
	if r.String("accessToken") != rejected {
		return tokens, nil
	}
	if !truthy(r.Get("refreshToken")) {
		return nil, nil
	}
	return s.refreshOnce(ctx, tokens)
}
func (s *Service) refreshOnce(ctx context.Context, tokens *Document) (*Document, error) {
	key := string(tokens.Record().Get("accessToken"))
	s.mu.Lock()
	p := s.refreshes[key]
	if p == nil {
		p = &pendingRefresh{done: make(chan struct{})}
		s.refreshes[key] = p
		go func() {
			value, err := s.refreshStored(context.Background(), tokens)
			if err != nil {
				if s.options.OnRefreshFailure != nil {
					s.options.OnRefreshFailure(expectedFailure(err), err)
				}
				value = nil
			}
			s.mu.Lock()
			p.value = value
			close(p.done)
			if s.refreshes[key] == p {
				delete(s.refreshes, key)
			}
			s.mu.Unlock()
		}()
	}
	s.mu.Unlock()
	select {
	case <-p.done:
		return p.value, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (s *Service) refreshStored(ctx context.Context, tokens *Document) (*Document, error) {
	old := tokens.Record()
	if !truthy(old.Get("refreshToken")) {
		return nil, errors.New("Claude OAuth refresh token is unavailable")
	}
	fresh, err := s.options.Refresh(ctx, old.String("refreshToken"), old.Get("scopes"))
	if err != nil {
		return nil, err
	}
	updated := record(protocol.JSONField{Name: "accessToken", Value: fresh.Get("accessToken")}, protocol.JSONField{Name: "refreshToken", Value: coalesce(fresh.Get("refreshToken"), old.Get("refreshToken"))}, protocol.JSONField{Name: "expiresAt", Value: fresh.Get("expiresAt")}, protocol.JSONField{Name: "scopes", Value: fresh.Get("scopes")}, protocol.JSONField{Name: "subscriptionType", Value: coalesce(fresh.Get("subscriptionType"), old.Get("subscriptionType"))})
	if err = s.SaveTokens(ctx, updated); err != nil {
		return nil, err
	}
	return &Document{encode(updated)}, nil
}
func (s *Service) EnsureFreshAccessToken(ctx context.Context) (*string, error) {
	tokens, err := s.EnsureFreshTokens(ctx)
	if err != nil || tokens == nil {
		return nil, err
	}
	if tokens.Record().Get("accessToken") == nil || string(tokens.Record().Get("accessToken")) == "null" {
		return nil, nil
	}
	v := tokens.Record().String("accessToken")
	return &v, nil
}
func object(data []byte) protocol.JSONObject {
	var o protocol.JSONObject
	_ = o.UnmarshalJSON(data)
	if !o.IsObject() {
		return record()
	}
	return o
}
func record(fields ...protocol.JSONField) protocol.JSONObject {
	return protocol.NewJSONObject(fields...)
}
func encode(value any) json.RawMessage {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if e.Encode(value) != nil {
		return nil
	}
	return protocol.StringifyValidatedJavaScriptJSON(bytes.TrimSpace(b.Bytes()))
}
func field(name string, value any) protocol.JSONField {
	return protocol.JSONField{Name: name, Value: encode(value)}
}
func coalesce(values ...json.RawMessage) json.RawMessage {
	for _, v := range values {
		if v != nil && string(v) != "null" {
			return v
		}
	}
	return []byte("null")
}
func truthy(data json.RawMessage) bool {
	var v any
	if json.Unmarshal(data, &v) != nil {
		return false
	}
	switch v := v.(type) {
	case nil:
		return false
	case bool:
		return v
	case float64:
		return v != 0
	case string:
		return v != ""
	default:
		return true
	}
}
func number(data json.RawMessage) float64 {
	if data == nil {
		return math.NaN()
	}
	var v any
	if json.Unmarshal(data, &v) != nil {
		return math.NaN()
	}
	switch v := v.(type) {
	case nil:
		return 0
	case float64:
		return v
	case bool:
		if v {
			return 1
		}
		return 0
	case string:
		if strings.TrimSpace(v) == "" {
			return 0
		}
		n, e := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if e == nil {
			return n
		}
	}
	return math.NaN()
}
func jsNumber(n float64) string {
	data := encode(n)
	if data == nil {
		return "NaN"
	}
	return string(data)
}
func expectedFailure(err error) bool {
	return regexp.MustCompile(`(?i)(refresh revoked|invalid_grant|\b401\b|\b403\b|unauthorized|forbidden)`).MatchString(err.Error())
}
