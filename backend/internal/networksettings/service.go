package networksettings

import (
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const DefaultTimeoutMillis = 1_800_000
const MinimumTimeoutMillis = 30_000
const MaximumTimeoutMillis = 2_147_483_000
const MinimumStreamMillis = 600_000

type Proxy struct {
	Mode string `json:"mode"`
	URL  string `json:"url"`
}
type Settings struct {
	TimeoutMillis int64 `json:"aiRequestTimeoutMs"`
	Proxy         Proxy `json:"proxy"`
}

func object(raw json.RawMessage) protocol.JSONObject {
	var value protocol.JSONObject
	_ = value.UnmarshalJSON(raw)
	return value
}
func space(r rune) bool {
	return r >= 9 && r <= 13 || r == 0x20 || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000 || r == 0xfeff
}
func trim(s string) string { return strings.TrimFunc(s, space) }

func Normalize(raw json.RawMessage) Settings {
	network := object(object(raw).Get("network"))
	result := Settings{TimeoutMillis: DefaultTimeoutMillis, Proxy: Proxy{Mode: "system"}}
	value := network.Get("aiRequestTimeoutMs")
	if number, e := strconv.ParseFloat(string(value), 64); e == nil && !math.IsNaN(number) && !math.IsInf(number, 0) {
		result.TimeoutMillis = int64(math.Min(math.Max(math.Floor(number+0.5), MinimumTimeoutMillis), MaximumTimeoutMillis))
	}
	proxy := object(network.Get("proxy"))
	mode := proxy.String("mode")
	if mode == "manual" || mode == "direct" || mode == "system" {
		result.Proxy.Mode = mode
	}
	if result.Proxy.Mode == "manual" {
		result.Proxy.URL = trim(proxy.String("url"))
	}
	return result
}

func ProxyURL(settings Settings, env map[string]string) (string, error) {
	switch settings.Proxy.Mode {
	case "manual":
		return trim(settings.Proxy.URL), nil
	case "system":
		if url := trim(env["CC_HAHA_SYSTEM_PROXY_URL"]); url != "" {
			return url, nil
		}
		if problem := trim(env["CC_HAHA_SYSTEM_PROXY_ERROR"]); problem != "" {
			return "", errors.New(problem)
		}
		for _, key := range []string{"https_proxy", "HTTPS_PROXY", "http_proxy", "HTTP_PROXY"} {
			if env[key] != "" {
				return env[key], nil
			}
		}
	}
	return "", nil
}

func MergeLoopback(existing string) string {
	entries := strings.FieldsFunc(existing, func(r rune) bool { return r == ',' || space(r) })
	seen := map[string]bool{}
	for _, entry := range entries {
		seen[strings.ToLower(trim(entry))] = true
	}
	for _, entry := range []string{"localhost", "127.0.0.1", "::1"} {
		if !seen[entry] {
			entries = append(entries, entry)
		}
	}
	return strings.Join(entries, ",")
}

func BuildEnvironment(settings Settings, base map[string]string) (map[string]string, error) {
	env := map[string]string{"API_TIMEOUT_MS": strconv.FormatInt(settings.TimeoutMillis, 10)}
	url := ""
	var e error
	if settings.Proxy.Mode != "direct" {
		url, e = ProxyURL(settings, base)
		if e != nil {
			return nil, e
		}
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy", "ALL_PROXY", "all_proxy"} {
		env[key] = url
	}
	if url != "" {
		existing := base["no_proxy"]
		if existing == "" {
			existing = base["NO_PROXY"]
		}
		env["NO_PROXY"] = MergeLoopback(existing)
		env["no_proxy"] = env["NO_PROXY"]
	}
	return env, nil
}
