package publicaccess

import (
	"regexp"
	"slices"
	"strings"
)

var socketSessionPattern = regexp.MustCompile(`^[0-9a-zA-Z_-]{1,64}$`)
var reservedProviderPaths = []string{"settings", "cc-switch", "test", "models", "presets", "auth-status", "official", "reorder"}

func ProviderAllowed(p []string, method string) bool {
	if len(p) < 3 {
		return method == "GET" || method == "POST"
	}
	id := p[2]
	if len(p) == 3 {
		switch id {
		case "presets", "auth-status":
			return method == "GET"
		case "official":
			return method == "POST"
		case "reorder":
			return method == "PUT"
		}
		return !slices.Contains(reservedProviderPaths, id) && slices.Contains([]string{"GET", "PUT", "DELETE"}, method)
	}
	return len(p) == 4 && !slices.Contains(reservedProviderPaths, id) && p[3] == "activate" && method == "POST"
}
func SettingsAllowed(p []string, method string) bool {
	return len(p) == 3 && p[2] == "user" && (method == "GET" || method == "PUT")
}
func BusinessAllowed(p []string, method string) bool {
	if len(p) == 0 {
		return false
	}
	if p[0] == "ws" {
		return len(p) == 2 && socketSessionPattern.MatchString(p[1])
	}
	if p[0] != "api" || len(p) < 2 {
		return false
	}
	if p[1] == "providers" {
		return ProviderAllowed(p, method)
	}
	if p[1] == "settings" {
		return SettingsAllowed(p, method)
	}
	path := "/" + strings.Join(p, "/")
	if method == "PUT" && (path == "/api/models/current" || path == "/api/effort") {
		return true
	}
	if method == "GET" && (path == "/api/permissions/mode") {
		return true
	}
	if p[1] == "sessions" || p[1] == "conversations" {
		return true
	}
	return method == "GET" && slices.Contains([]string{"models", "effort", "search", "agents", "tasks", "teams", "activity-stats"}, p[1])
}
