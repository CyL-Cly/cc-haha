// Package h5access owns the existing LAN access settings and token service.
// Host discovery is injected so callers choose the process environment and
// network boundary; this package never discovers a user's config directory.
package h5access

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/access"
	weburl "github.com/nlnwa/whatwg-url/url"
)

type InterfaceAddress struct {
	Family   string `json:"family"`
	Internal bool   `json:"internal"`
	Address  string `json:"address"`
}

// A slice preserves JS Object.entries order, including ties in LAN scoring.
type NetworkInterface struct {
	Name      string             `json:"name"`
	Addresses []InterfaceAddress `json:"entries"`
}

func CollectLocalIPv4Hosts(interfaces []NetworkInterface) []string {
	hosts := []string{}
	for _, network := range interfaces {
		for _, entry := range network.Addresses {
			if entry.Family == "IPv4" && !entry.Internal {
				hosts = append(hosts, entry.Address)
			}
		}
	}
	return hosts
}

var decimalOctet = regexp.MustCompile(`^\d+$`)
var physicalInterface = regexp.MustCompile(`(?i)\b(wi-?fi|wlan|wireless|ethernet|lan|en\d+|eth\d+)\b`)
var virtualInterface = regexp.MustCompile(`(?i)\b(wsl|docker|hyper-?v|veth|vethernet|virtual|virtualbox|vmware|podman|container|bridge|br-|tailscale|zerotier|utun|vpn)\b`)

func privateIPv4Parts(address string) (float64, float64, bool) {
	parts := strings.Split(address, ".")
	if len(parts) != 4 {
		return 0, 0, false
	}
	for _, part := range parts {
		if !decimalOctet.MatchString(part) {
			return 0, 0, false
		}
	}
	a, _ := strconv.ParseFloat(parts[0], 64)
	b, _ := strconv.ParseFloat(parts[1], 64)
	return a, b, true
}

func is172PrivateIPv4(address string) bool {
	a, b, valid := privateIPv4Parts(address)
	return valid && a == 172 && b >= 16 && b <= 31
}

func isPrivateIPv4(address string) bool {
	a, b, valid := privateIPv4Parts(address)
	return valid && (a == 10 || a == 172 && b >= 16 && b <= 31 || a == 192 && b == 168 || a == 169 && b == 254)
}

func FindPrivateLanAddress(interfaces []NetworkInterface) *string {
	type candidate struct {
		address string
		score   int
	}
	var candidates []candidate
	for _, network := range interfaces {
		for _, entry := range network.Addresses {
			if entry.Family != "IPv4" || entry.Internal || !isPrivateIPv4(entry.Address) {
				continue
			}
			score := 0
			if physicalInterface.MatchString(network.Name) {
				score += 100
			}
			if virtualInterface.MatchString(network.Name) {
				score -= 200
			}
			if strings.HasPrefix(entry.Address, "192.168.") {
				score += 30
			} else if strings.HasPrefix(entry.Address, "10.") {
				score += 20
			} else if is172PrivateIPv4(entry.Address) {
				score += 10
			} else if strings.HasPrefix(entry.Address, "169.254.") {
				score -= 100
			}
			candidates = append(candidates, candidate{entry.Address, score})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].score > candidates[j].score })
	if len(candidates) == 0 {
		return nil
	}
	return &candidates[0].address
}

func preferredLanHost(hosts []string) *string {
	for _, prefix := range []string{"192.168.", "10."} {
		for _, host := range hosts {
			if strings.HasPrefix(host, prefix) {
				return &host
			}
		}
	}
	for _, host := range hosts {
		if is172PrivateIPv4(host) {
			return &host
		}
	}
	if len(hosts) == 0 {
		return nil
	}
	return &hosts[0]
}

func ClassifyPublicBaseURL(value string) string {
	u, err := weburl.Parse(value)
	if err != nil || u.Scheme() != "http" && u.Scheme() != "https" || u.Username() != "" || u.Password() != "" {
		return "invalid"
	}
	if u.Scheme() == "http" && strings.TrimRight(u.Pathname(), "/") == "" && isPrivateIPv4(u.Hostname()) {
		return "plain-lan"
	}
	return "proxy"
}

type ValidationResult struct {
	OK            bool    `json:"ok"`
	Kind          string  `json:"kind,omitempty"`
	Reason        string  `json:"reason,omitempty"`
	SuggestedHost *string `json:"suggestedHost,omitempty"`
}

// ValidationResultJSON keeps suggestedHost:null on the rejected branch while
// omitting the key on success, matching the source discriminated union.
func (result ValidationResult) MarshalJSON() ([]byte, error) {
	if result.OK {
		return jsonMarshal(struct {
			OK   bool   `json:"ok"`
			Kind string `json:"kind"`
		}{true, result.Kind})
	}
	return jsonMarshal(struct {
		OK            bool    `json:"ok"`
		Reason        string  `json:"reason"`
		SuggestedHost *string `json:"suggestedHost"`
	}{false, result.Reason, result.SuggestedHost})
}

func ValidatePublicBaseURL(value string, hosts []string) ValidationResult {
	kind := ClassifyPublicBaseURL(value)
	if kind == "invalid" {
		return ValidationResult{Reason: "Invalid H5 publicBaseUrl: " + value, SuggestedHost: preferredLanHost(hosts)}
	}
	if kind == "plain-lan" {
		u, _ := weburl.Parse(value)
		if !contains(hosts, u.Hostname()) {
			available := strings.Join(hosts, ", ")
			if len(hosts) == 0 {
				available = "none"
			}
			return ValidationResult{Reason: fmt.Sprintf("H5 host %s is not bound to any local network interface on this machine. Available LAN IPv4: %s", u.Hostname(), available), SuggestedHost: preferredLanHost(hosts)}
		}
	}
	return ValidationResult{OK: true, Kind: kind}
}

type EffectiveURLInput struct {
	Enabled                 bool    `json:"enabled"`
	StoredPublicBaseURL     *string `json:"storedPublicBaseUrl"`
	ConfiguredPublicBaseURL *string `json:"configuredPublicBaseUrl"`
	AutoPublicBaseURL       *string `json:"autoPublicBaseUrl"`
	// nil retains undefined; an empty but non-nil slice participates in stale
	// host detection, just as the source's Array.isArray check does.
	LocalInterfaceHosts []string `json:"localInterfaceHosts"`
}

func ResolveEffectivePublicBaseURL(input EffectiveURLInput) *string {
	if !input.Enabled {
		return input.StoredPublicBaseURL
	}
	if truthyString(input.ConfiguredPublicBaseURL) {
		return input.ConfiguredPublicBaseURL
	}
	if !truthyString(input.AutoPublicBaseURL) {
		return input.StoredPublicBaseURL
	}
	if !truthyString(input.StoredPublicBaseURL) || isLocalPublicBaseURL(*input.StoredPublicBaseURL) {
		return input.AutoPublicBaseURL
	}
	if input.LocalInterfaceHosts != nil && ClassifyPublicBaseURL(*input.StoredPublicBaseURL) == "plain-lan" {
		u, _ := weburl.Parse(*input.StoredPublicBaseURL)
		if !contains(input.LocalInterfaceHosts, u.Hostname()) {
			return input.AutoPublicBaseURL
		}
	}
	stored, storedErr := weburl.Parse(*input.StoredPublicBaseURL)
	auto, autoErr := weburl.Parse(*input.AutoPublicBaseURL)
	if storedErr == nil && autoErr == nil && stored.Scheme() == "http" && strings.TrimRight(stored.Pathname(), "/") == "" && isPrivateIPv4(stored.Hostname()) && auto.Port() != "" {
		refreshed := "http://" + stored.Hostname() + ":" + auto.Port()
		return &refreshed
	}
	return input.StoredPublicBaseURL
}

func isLocalPublicBaseURL(value string) bool {
	u, err := weburl.Parse(value)
	if err != nil {
		return false
	}
	switch access.NormalizeHostname(u.Hostname()) {
	case "localhost", "127.0.0.1", "::1", "0.0.0.0", "::":
		return true
	}
	return false
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
func truthyString(value *string) bool { return value != nil && *value != "" }
