package cloudauth

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

var ErrGCPAuthCapability = errors.New("Google credential capability is not migrated")

const GoogleTokenURL = "https://oauth2.googleapis.com/token"
const CloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"

type GoogleOptions struct {
	Environment         map[string]string
	Home, CWD, Platform string
	ReadFile            func(context.Context, string) ([]byte, error)
	Do                  HTTPDo
	Now                 func() time.Time
	// ProjectFallback is the actual GoogleAuth constructor option, not a
	// synthetic replacement for key-file, gcloud or metadata project discovery.
	ProjectFallback string
}
type GoogleAuth struct {
	options   GoogleOptions
	client    *GoogleClient
	projectID string
}
type GoogleClient struct {
	options                                                GoogleOptions
	kind, email, key, clientID, clientSecret, refreshToken string
	emailJSON                                              json.RawMessage
	ProjectID, QuotaProjectID                              string
	credentials                                            protocol.JSONObject
}

func NewGoogleAuth(options GoogleOptions) (*GoogleAuth, error) {
	if options.Home == "" || !filepath.IsAbs(options.Home) || options.ReadFile == nil || options.Do == nil {
		return nil, errors.New("Google auth requires explicit home, file and HTTP primitives")
	}
	env := map[string]string{}
	for key, value := range options.Environment {
		env[key] = value
	}
	options.Environment = env
	if options.Now == nil {
		options.Now = time.Now
	}
	return &GoogleAuth{options: options, projectID: options.ProjectFallback}, nil
}
func (g *GoogleAuth) GetClient(ctx context.Context) (*GoogleClient, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if g.client != nil {
		return g.client, nil
	}
	env := g.options.Environment
	path := env["GOOGLE_APPLICATION_CREDENTIALS"]
	if path == "" {
		path = env["google_application_credentials"]
	}
	explicit := path != ""
	if path == "" {
		base := env["HOME"]
		if g.options.Platform == "win32" {
			base = env["APPDATA"]
		} else if base != "" {
			base = filepath.Join(base, ".config")
		}
		if base != "" {
			path = filepath.Join(base, "gcloud", "application_default_credentials.json")
		}
	}
	if path == "" {
		return nil, fmt.Errorf("%w: GCE ADC discovery", ErrGCPAuthCapability)
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(g.options.CWD, path)
	}
	raw, err := g.options.ReadFile(ctx, path)
	if err != nil {
		if !explicit && errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: GCE ADC discovery", ErrGCPAuthCapability)
		}
		return nil, err
	}
	var data protocol.JSONObject
	if err = json.Unmarshal(raw, &data); err != nil || !data.IsObject() {
		return nil, fmt.Errorf("%w: ADC JSON syntax diagnostics", ErrGCPAuthCapability)
	}
	kind := data.String("type")
	if kind == "external_account" || kind == "external_account_authorized_user" || kind == "impersonated_service_account" {
		return nil, fmt.Errorf("%w: %s", ErrGCPAuthCapability, kind)
	}
	if universe := data.String("universe_domain"); universe != "" && universe != "googleapis.com" {
		return nil, fmt.Errorf("%w: alternate universe JWT", ErrGCPAuthCapability)
	}
	client := &GoogleClient{options: g.options, kind: kind, credentials: protocol.NewJSONObject()}
	if kind == "authorized_user" {
		for _, key := range []string{"client_id", "client_secret", "refresh_token"} {
			if !jsonTruthyCloud(data.Get(key)) {
				return nil, errors.New("The incoming JSON object does not contain a " + key + " field")
			}
			if len(data.Get(key)) == 0 || data.Get(key)[0] != '"' {
				return nil, fmt.Errorf("%w: ADC field coercion %s", ErrGCPAuthCapability, key)
			}
		}
		client.clientID, client.clientSecret, client.refreshToken = data.String("client_id"), data.String("client_secret"), data.String("refresh_token")
	} else {
		client.kind = "service_account"
		for _, key := range []string{"client_email", "private_key"} {
			if !jsonTruthyCloud(data.Get(key)) {
				return nil, errors.New("The incoming JSON object does not contain a " + key + " field")
			}
			if len(data.Get(key)) == 0 || data.Get(key)[0] != '"' {
				return nil, fmt.Errorf("%w: ADC field coercion %s", ErrGCPAuthCapability, key)
			}
		}
		client.email, client.key = data.String("client_email"), data.String("private_key")
		client.emailJSON = data.Get("client_email")
		client.ProjectID = data.String("project_id")
	}
	client.QuotaProjectID = data.String("quota_project_id")
	if value := env["GOOGLE_CLOUD_QUOTA_PROJECT"]; value != "" {
		client.QuotaProjectID = value
	}
	if g.projectID == "" {
		for _, key := range []string{"GCLOUD_PROJECT", "GOOGLE_CLOUD_PROJECT", "gcloud_project", "google_cloud_project"} {
			if env[key] != "" {
				g.projectID = env[key]
				break
			}
		}
	}
	if g.projectID == "" {
		g.projectID = client.ProjectID
	}
	if g.projectID == "" {
		return nil, fmt.Errorf("%w: ADC gcloud/metadata project discovery", ErrGCPAuthCapability)
	}
	g.client = client
	return client, nil
}
func (g *GoogleAuth) ProjectID(ctx context.Context) (string, error) {
	if _, err := g.GetClient(ctx); err != nil {
		return "", err
	}
	return g.projectID, nil
}
func (c *GoogleClient) Kind() string {
	if c.kind == "authorized_user" {
		return "UserRefreshClient"
	}
	return "JWT"
}
func (c *GoogleClient) GetRequestHeaders(ctx context.Context) (http.Header, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	token := c.credentials.String("access_token")
	expiry := 0.
	_ = json.Unmarshal(c.credentials.Get("expiry_date"), &expiry)
	if token != "" && (expiry == 0 || expiry > float64(c.options.Now().UnixMilli())+300000) {
		kind := c.credentials.String("token_type")
		if kind == "" {
			kind = "Bearer"
		}
		return c.headers(kind + " " + token), nil
	}
	oldType := c.credentials.String("token_type")
	if oldType == "" {
		oldType = "Bearer"
	}
	fields := [][2]string{}
	if c.kind == "authorized_user" {
		fields = [][2]string{{"refresh_token", c.refreshToken}, {"client_id", c.clientID}, {"client_secret", c.clientSecret}, {"grant_type", "refresh_token"}}
	} else {
		assertion, err := c.assertion()
		if err != nil {
			return nil, err
		}
		fields = [][2]string{{"grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer"}, {"assertion", assertion}}
	}
	parts := []string{}
	for _, field := range fields {
		parts = append(parts, url.QueryEscape(field[0])+"="+url.QueryEscape(field[1]))
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, GoogleTokenURL, strings.NewReader(strings.Join(parts, "&")))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := c.options.Do(request)
	if err != nil {
		return nil, err
	}
	if response == nil || response.Body == nil {
		return nil, errors.New("Google auth transport returned no body")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > 2*1024*1024 {
		return nil, fmt.Errorf("%w: bounded Google token response", ErrGCPAuthCapability)
	}
	var data protocol.JSONObject
	if json.Unmarshal(raw, &data) != nil || !data.IsObject() {
		return nil, fmt.Errorf("%w: Google token JSON syntax diagnostics", ErrGCPAuthCapability)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: Gaxios OAuth response/retry", ErrGCPAuthCapability)
	}
	if c.kind == "authorized_user" {
		if jsonTruthyCloud(data.Get("expires_in")) {
			var seconds float64
			if json.Unmarshal(data.Get("expires_in"), &seconds) != nil {
				return nil, fmt.Errorf("%w: Google expires_in coercion", ErrGCPAuthCapability)
			}
			stamp, _ := json.Marshal(float64(c.options.Now().UnixMilli()) + seconds*1000)
			data.Set("expiry_date", stamp)
			data.Delete("expires_in")
		}
		data.Set("refresh_token", mustCloudString(c.refreshToken))
		c.credentials = data
	} else {
		var seconds float64
		_ = json.Unmarshal(data.Get("expires_in"), &seconds)
		stamp, _ := json.Marshal(float64(c.options.Now().UnixMilli()) + seconds*1000)
		c.credentials = protocol.NewJSONObject(protocol.JSONField{Name: "access_token", Value: data.Get("access_token")}, protocol.JSONField{Name: "token_type", Value: mustCloudString("Bearer")}, protocol.JSONField{Name: "expiry_date", Value: stamp}, protocol.JSONField{Name: "refresh_token", Value: mustCloudString("jwt-placeholder")})
	}
	return c.headers(oldType + " " + c.credentials.String("access_token")), nil
}
func (c *GoogleClient) headers(auth string) http.Header {
	headers := http.Header{}
	headers.Set("authorization", auth)
	if c.QuotaProjectID != "" {
		headers.Set("x-goog-user-project", c.QuotaProjectID)
	}
	return headers
}
func (c *GoogleClient) assertion() (string, error) {
	block, _ := pem.Decode([]byte(c.key))
	if block == nil {
		return "", fmt.Errorf("%w: private key diagnostics", ErrGCPAuthCapability)
	}
	var private *rsa.PrivateKey
	if value, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		private, _ = value.(*rsa.PrivateKey)
	} else {
		private, _ = x509.ParsePKCS1PrivateKey(block.Bytes)
	}
	if private == nil {
		return "", fmt.Errorf("%w: non-RSA/invalid private key", ErrGCPAuthCapability)
	}
	now := c.options.Now().Unix()
	payload := protocol.NewJSONObject(protocol.JSONField{Name: "iss", Value: c.emailJSON}, protocol.JSONField{Name: "scope", Value: mustCloudString(CloudPlatformScope)}, protocol.JSONField{Name: "aud", Value: mustCloudString(GoogleTokenURL)}, protocol.JSONField{Name: "exp", Value: json.RawMessage(fmt.Sprintf("%d", now+3600))}, protocol.JSONField{Name: "iat", Value: json.RawMessage(fmt.Sprintf("%d", now))})
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	raw, err = protocol.ParseAndStringifyJavaScriptJSON(raw)
	if err != nil {
		return "", err
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256"}`))
	signed := header + "." + base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(signed))
	signature, err := rsa.SignPKCS1v15(rand.Reader, private, crypto.SHA256, hash[:])
	if err != nil {
		return "", err
	}
	return signed + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}
func mustCloudString(value string) json.RawMessage { raw, _ := json.Marshal(value); return raw }
func jsonTruthyCloud(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) || bytes.Equal(raw, []byte("false")) {
		return false
	}
	if raw[0] == '"' {
		units, err := protocol.DecodeJavaScriptJSONString(raw)
		return err == nil && len(units) > 0
	}
	if raw[0] == '{' || raw[0] == '[' || raw[0] == 't' {
		return true
	}
	var number float64
	return json.Unmarshal(raw, &number) == nil && number != 0
}
