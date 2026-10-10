package h5access

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	weburl "github.com/nlnwa/whatwg-url/url"
)

const DefaultDisconnectGraceMS = 30000

type PublicSettings struct {
	Enabled                bool            `json:"enabled"`
	Token                  *string         `json:"token"`
	TokenPreview           json.RawMessage `json:"tokenPreview"`
	AllowedOrigins         []string        `json:"allowedOrigins"`
	FixedPort              *int            `json:"fixedPort"`
	DisconnectGraceSeconds *int            `json:"disconnectGraceSeconds"`
	PublicBaseURL          *string         `json:"publicBaseUrl"`
}

type storedSettings struct {
	Enabled                bool            `json:"enabled"`
	Token                  *string         `json:"token"`
	TokenHash              *string         `json:"tokenHash"`
	TokenPreview           json.RawMessage `json:"tokenPreview"`
	AllowedOrigins         []string        `json:"allowedOrigins"`
	PublicBaseURL          *string         `json:"publicBaseUrl"`
	FixedPort              *int            `json:"fixedPort"`
	DisconnectGraceSeconds *int            `json:"disconnectGraceSeconds"`
}

type EnableResult struct {
	Settings PublicSettings `json:"settings"`
	Token    string         `json:"token"`
}

type Diagnostics struct {
	StoredHostStaleness    string   `json:"storedHostStaleness"`
	StoredPublicBaseURL    *string  `json:"storedPublicBaseUrl"`
	EffectivePublicBaseURL *string  `json:"effectivePublicBaseUrl"`
	SuggestedHost          *string  `json:"suggestedHost"`
	LocalInterfaceHosts    []string `json:"localInterfaceHosts"`
	ActivePort             int      `json:"activePort"`
}

type Options struct {
	Managed           *settings.ManagedService
	Environment       func() map[string]string
	NetworkInterfaces func() []NetworkInterface
	ServerPort        func() int
	Random            io.Reader
}

type Service struct{ options Options }

func New(options Options) (*Service, error) {
	if options.Managed == nil || options.Environment == nil || options.NetworkInterfaces == nil || options.ServerPort == nil {
		return nil, errors.New("H5 access bootstrap requires managed settings, explicit environment, network interfaces and server port")
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	return &Service{options: options}, nil
}

func jsonMarshal(value any) ([]byte, error) {
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	err := encoder.Encode(value)
	return bytes.TrimSuffix(output.Bytes(), []byte("\n")), err
}

func badRequest(message string) error {
	return &settings.APIError{StatusCode: 400, Code: "BAD_REQUEST", Message: message}
}
func stringValue(raw json.RawMessage) (*string, bool) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, false
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return nil, false
	}
	return &value, true
}

func normalizeOrigin(origin, field string) (string, error) {
	if strings.Contains(origin, "*") {
		return "", badRequest(field + " must not contain wildcard origins")
	}
	u, err := weburl.Parse(origin)
	if err != nil {
		return "", badRequest("Invalid origin: " + origin)
	}
	if u.Scheme() != "http" && u.Scheme() != "https" {
		return "", badRequest("Invalid origin protocol: " + origin)
	}
	if u.Username() != "" || u.Password() != "" {
		return "", badRequest("Invalid origin credentials: " + origin)
	}
	return u.Scheme() + "://" + u.Host(), nil
}

func normalizeAllowedOrigins(raw json.RawMessage) ([]string, error) {
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) != nil || values == nil {
		return nil, badRequest("allowedOrigins must be an array of strings")
	}
	result := []string{}
	for _, value := range values {
		text, valid := stringValue(value)
		if !valid {
			return nil, badRequest("allowedOrigins must be an array of strings")
		}
		normalized, err := normalizeOrigin(*text, "allowedOrigins")
		if err != nil {
			return nil, err
		}
		if !contains(result, normalized) {
			result = append(result, normalized)
		}
	}
	return result, nil
}

func normalizePublicBaseURL(raw json.RawMessage) (*string, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	value, valid := stringValue(raw)
	if !valid {
		return nil, badRequest("publicBaseUrl must be a string or null")
	}
	if *value == "" {
		return nil, nil
	}
	u, err := weburl.Parse(*value)
	if err != nil {
		return nil, badRequest("Invalid publicBaseUrl: " + *value)
	}
	if u.Scheme() != "http" && u.Scheme() != "https" {
		return nil, badRequest("Invalid publicBaseUrl protocol: " + *value)
	}
	if u.Username() != "" || u.Password() != "" {
		return nil, badRequest("Invalid publicBaseUrl credentials: " + *value)
	}
	result := u.Scheme() + "://" + u.Host() + strings.TrimRight(u.Pathname(), "/")
	return &result, nil
}

var tokenHashRE = regexp.MustCompile(`^[a-f0-9]{64}$`)

func validToken(token string) bool {
	if len(token) < 16 || len(token) > 512 {
		return false
	}
	for _, character := range []byte(token) {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
func tokenPreview(token string) string { return token[:7] + "..." + token[len(token)-4:] }

var blockedPorts = map[int]bool{
	0: true, 1: true, 7: true, 9: true, 11: true, 13: true, 15: true, 17: true, 19: true, 20: true, 21: true, 22: true, 23: true, 25: true, 37: true, 42: true, 43: true, 53: true,
	69: true, 77: true, 79: true, 87: true, 95: true, 101: true, 102: true, 103: true, 104: true, 109: true, 110: true, 111: true, 113: true, 115: true, 117: true,
	119: true, 123: true, 135: true, 137: true, 139: true, 143: true, 161: true, 179: true, 389: true, 427: true, 465: true, 512: true, 513: true, 514: true,
	515: true, 526: true, 530: true, 531: true, 532: true, 540: true, 548: true, 554: true, 556: true, 563: true, 587: true, 601: true, 636: true, 989: true,
	990: true, 993: true, 995: true, 1719: true, 1720: true, 1723: true, 2049: true, 3659: true, 4045: true, 4190: true, 5060: true, 5061: true,
	6000: true, 6566: true, 6665: true, 6666: true, 6667: true, 6668: true, 6669: true, 6679: true, 6697: true, 10080: true,
}

func IsBrowserSafePort(port float64) bool {
	return !math.IsNaN(port) && !math.IsInf(port, 0) && math.Trunc(port) == port && port > 0 && port <= 65535 && !blockedPorts[int(port)]
}

func numberValue(raw json.RawMessage) float64 {
	var number json.Number
	if json.Unmarshal(raw, &number) != nil || len(raw) == 0 || bytes.TrimSpace(raw)[0] == '"' {
		return math.NaN()
	}
	value, _ := strconv.ParseFloat(string(number), 64)
	if number == "" {
		return math.NaN()
	}
	return value
}

func normalizeFixedPort(raw json.RawMessage) *int {
	value := numberValue(raw)
	if value < 1024 || !IsBrowserSafePort(value) {
		return nil
	}
	port := int(value)
	return &port
}

func normalizeGrace(raw json.RawMessage) *int {
	value := numberValue(raw)
	if math.IsNaN(value) || math.IsInf(value, 0) || math.Trunc(value) != value || value < 5 || value > 86400 {
		return nil
	}
	seconds := int(value)
	return &seconds
}

func normalizeStored(raw json.RawMessage) storedSettings {
	value, _, _ := storage.NormalizeObject(raw)
	result := storedSettings{AllowedOrigins: []string{}}
	var origins []json.RawMessage
	if json.Unmarshal(value["allowedOrigins"], &origins) == nil {
		for _, origin := range origins {
			if text, valid := stringValue(origin); valid {
				if normalized, err := normalizeOrigin(*text, "allowedOrigins"); err == nil && !contains(result.AllowedOrigins, normalized) {
					result.AllowedOrigins = append(result.AllowedOrigins, normalized)
				}
			}
		}
	}
	if _, valid := stringValue(value["publicBaseUrl"]); valid {
		result.PublicBaseURL, _ = normalizePublicBaseURL(value["publicBaseUrl"])
	}
	if token, valid := stringValue(value["token"]); valid && validToken(*token) {
		result.Token = token
		hash := hashToken(*token)
		result.TokenHash = &hash
		preview := tokenPreview(*token)
		result.TokenPreview, _ = jsonMarshal(preview)
	} else if hash, valid := stringValue(value["tokenHash"]); valid && tokenHashRE.MatchString(*hash) {
		result.TokenHash = hash
		if _, valid := stringValue(value["tokenPreview"]); valid {
			result.TokenPreview = append(json.RawMessage(nil), value["tokenPreview"]...)
		}
	}
	result.Enabled = bytes.Equal(bytes.TrimSpace(value["enabled"]), []byte("true")) && result.TokenHash != nil
	result.FixedPort = normalizeFixedPort(value["fixedPort"])
	result.DisconnectGraceSeconds = normalizeGrace(value["disconnectGraceSeconds"])
	return result
}

func (service *Service) configuredPublicBaseURL() *string {
	value := service.options.Environment()["CLAUDE_H5_PUBLIC_BASE_URL"]
	if value == "" {
		return nil
	}
	raw, _ := jsonMarshal(value)
	result, _ := normalizePublicBaseURL(raw)
	return result
}

func (service *Service) autoPublicBaseURL() *string {
	if service.options.Environment()["CLAUDE_H5_AUTO_PUBLIC_URL"] != "1" {
		return nil
	}
	host := FindPrivateLanAddress(service.options.NetworkInterfaces())
	if host == nil {
		return nil
	}
	result := "http://" + *host + ":" + strconv.Itoa(service.options.ServerPort())
	return &result
}

func (service *Service) publicSettings(stored storedSettings) PublicSettings {
	configured := service.configuredPublicBaseURL()
	auto := service.autoPublicBaseURL()
	hosts := CollectLocalIPv4Hosts(service.options.NetworkInterfaces())
	return PublicSettings{Enabled: stored.Enabled, Token: stored.Token, TokenPreview: stored.TokenPreview, AllowedOrigins: stored.AllowedOrigins, FixedPort: stored.FixedPort, DisconnectGraceSeconds: stored.DisconnectGraceSeconds,
		PublicBaseURL: ResolveEffectivePublicBaseURL(EffectiveURLInput{Enabled: stored.Enabled, StoredPublicBaseURL: stored.PublicBaseURL, ConfiguredPublicBaseURL: configured, AutoPublicBaseURL: auto, LocalInterfaceHosts: hosts})}
}

func (service *Service) read(ctx context.Context) (storedSettings, error) {
	managed, err := service.options.Managed.ReadSettings(ctx)
	if err != nil {
		return storedSettings{}, err
	}
	return normalizeStored(managed["h5Access"]), nil
}

func (service *Service) GetSettings(ctx context.Context) (PublicSettings, error) {
	stored, err := service.read(ctx)
	if err != nil {
		return PublicSettings{}, err
	}
	return service.publicSettings(stored), nil
}

func persist[T any](current storage.Object, next storedSettings, result T) (settings.ManagedUpdate[T], error) {
	raw, err := jsonMarshal(next)
	if err != nil {
		return settings.ManagedUpdate[T]{}, err
	}
	current["h5Access"] = raw
	return settings.ManagedUpdate[T]{Settings: current, Result: result}, nil
}

func (service *Service) setToken(ctx context.Context, reuse bool) (EnableResult, error) {
	return settings.UpdateManaged(ctx, service.options.Managed, func(current storage.Object) (settings.ManagedUpdate[EnableResult], error) {
		next := normalizeStored(current["h5Access"])
		var token string
		if reuse && next.Token != nil {
			token = *next.Token
		} else {
			var random [32]byte
			if _, err := io.ReadFull(service.options.Random, random[:]); err != nil {
				return settings.ManagedUpdate[EnableResult]{}, err
			}
			token = "h5_" + base64.RawURLEncoding.EncodeToString(random[:])
		}
		hash := hashToken(token)
		preview := tokenPreview(token)
		next.Enabled = true
		next.Token = &token
		next.TokenHash = &hash
		next.TokenPreview, _ = jsonMarshal(preview)
		return persist(current, next, EnableResult{Settings: service.publicSettings(next), Token: token})
	})
}

func (service *Service) Enable(ctx context.Context) (EnableResult, error) {
	return service.setToken(ctx, true)
}
func (service *Service) RegenerateToken(ctx context.Context) (EnableResult, error) {
	return service.setToken(ctx, false)
}
func (service *Service) Disable(ctx context.Context) (PublicSettings, error) {
	return settings.UpdateManaged(ctx, service.options.Managed, func(current storage.Object) (settings.ManagedUpdate[PublicSettings], error) {
		next := normalizeStored(current["h5Access"])
		next.Enabled = false
		return persist(current, next, service.publicSettings(next))
	})
}

// A raw object preserves the distinction between omitted, null, and invalid
// typed fields, which plain nullable Go pointers cannot express at this seam.
func (service *Service) UpdateSettings(ctx context.Context, input storage.Object) (PublicSettings, error) {
	return settings.UpdateManaged(ctx, service.options.Managed, func(current storage.Object) (settings.ManagedUpdate[PublicSettings], error) {
		next := normalizeStored(current["h5Access"])
		if raw, exists := input["publicBaseUrl"]; exists {
			value, err := normalizePublicBaseURL(raw)
			if err != nil {
				return settings.ManagedUpdate[PublicSettings]{}, err
			}
			if value != nil {
				validation := ValidatePublicBaseURL(*value, CollectLocalIPv4Hosts(service.options.NetworkInterfaces()))
				if !validation.OK {
					return settings.ManagedUpdate[PublicSettings]{}, badRequest(validation.Reason)
				}
			}
			next.PublicBaseURL = value
		}
		if raw, exists := input["fixedPort"]; exists {
			next.FixedPort = normalizeFixedPort(raw)
			if next.FixedPort == nil && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				return settings.ManagedUpdate[PublicSettings]{}, badRequest("fixedPort must be a browser-safe integer between 1024 and 65535")
			}
		}
		if raw, exists := input["disconnectGraceSeconds"]; exists {
			next.DisconnectGraceSeconds = normalizeGrace(raw)
			if next.DisconnectGraceSeconds == nil && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				return settings.ManagedUpdate[PublicSettings]{}, badRequest("disconnectGraceSeconds must be an integer between 5 and 86400")
			}
		}
		if raw, exists := input["allowedOrigins"]; exists {
			origins, err := normalizeAllowedOrigins(raw)
			if err != nil {
				return settings.ManagedUpdate[PublicSettings]{}, err
			}
			next.AllowedOrigins = origins
		}
		return persist(current, next, service.publicSettings(next))
	})
}

func (service *Service) GetDiagnostics(ctx context.Context) (Diagnostics, error) {
	stored, err := service.read(ctx)
	if err != nil {
		return Diagnostics{}, err
	}
	hosts := CollectLocalIPv4Hosts(service.options.NetworkInterfaces())
	auto := service.autoPublicBaseURL()
	configured := service.configuredPublicBaseURL()
	staleness := "unset"
	if truthyString(stored.PublicBaseURL) {
		kind := ClassifyPublicBaseURL(*stored.PublicBaseURL)
		if kind == "plain-lan" {
			u, _ := weburl.Parse(*stored.PublicBaseURL)
			if contains(hosts, u.Hostname()) {
				staleness = "ok"
			} else {
				staleness = "unreachable"
			}
		} else if kind == "proxy" {
			staleness = "proxy"
		} else {
			staleness = "unreachable"
		}
	}
	return Diagnostics{StoredHostStaleness: staleness, StoredPublicBaseURL: stored.PublicBaseURL, EffectivePublicBaseURL: ResolveEffectivePublicBaseURL(EffectiveURLInput{Enabled: stored.Enabled, StoredPublicBaseURL: stored.PublicBaseURL, ConfiguredPublicBaseURL: configured, AutoPublicBaseURL: auto, LocalInterfaceHosts: hosts}), SuggestedHost: preferredLanHost(hosts), LocalInterfaceHosts: hosts, ActivePort: service.options.ServerPort()}, nil
}

func (service *Service) GetDisconnectGraceMS(ctx context.Context) (int, error) {
	stored, err := service.read(ctx)
	if err != nil {
		return 0, err
	}
	if stored.DisconnectGraceSeconds == nil {
		return DefaultDisconnectGraceMS, nil
	}
	return *stored.DisconnectGraceSeconds * 1000, nil
}

func (service *Service) ValidateToken(ctx context.Context, token string) (bool, error) {
	if token == "" {
		return false, nil
	}
	stored, err := service.read(ctx)
	if err != nil {
		return false, err
	}
	return stored.Enabled && stored.TokenHash != nil && hashToken(token) == *stored.TokenHash, nil
}

func (service *Service) IsOriginAllowed(ctx context.Context, origin string) (bool, error) {
	if origin == "" {
		return false, nil
	}
	stored, err := service.read(ctx)
	if err != nil {
		return false, err
	}
	if !stored.Enabled {
		return false, nil
	}
	normalized, err := normalizeOrigin(origin, "origin")
	return err == nil && contains(stored.AllowedOrigins, normalized), nil
}
