package pluginregistry

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/dlclark/regexp2/v2"
	whatwg "github.com/nlnwa/whatwg-url/url"
)

type Policy struct {
	Allowlist, Blocklist []protocol.JSONObject
	HasAllowlist         bool
}

func PolicyFromSettings(settings protocol.JSONObject) Policy {
	p := Policy{}
	if value := settings.Get("strictKnownMarketplaces"); truthy(value) {
		p.HasAllowlist = true
		_ = json.Unmarshal(value, &p.Allowlist)
	}
	if value := settings.Get("blockedMarketplaces"); truthy(value) {
		_ = json.Unmarshal(value, &p.Blocklist)
	}
	return p
}
func (p Policy) Active() bool { return p.HasAllowlist || len(p.Blocklist) > 0 }
func sourceHost(source protocol.JSONObject) string {
	kind := source.String("source")
	if kind == "github" {
		return "github.com"
	}
	value := source.String("url")
	if kind == "git" {
		m := regexp.MustCompile(`^[^@]+@([^:]+):`).FindStringSubmatch(value)
		if len(m) > 1 {
			return m[1]
		}
	}
	if kind == "git" || kind == "url" {
		parsed, e := whatwg.NewParser().Parse(value)
		if e == nil {
			return parsed.Hostname()
		}
	}
	return ""
}
func patternMatches(pattern, value string) bool {
	compiled, e := regexp2.Compile(pattern, regexp2.ECMAScript)
	if e != nil {
		return false
	}
	match, _ := compiled.MatchString(value)
	return match
}
func gitHubRepo(value string) string {
	for _, pattern := range []string{`^git@github\.com:([^/]+/[^/]+?)(?:\.git)?$`, `^https?://github\.com/([^/]+/[^/]+?)(?:\.git)?$`} {
		m := regexp.MustCompile(pattern).FindStringSubmatch(value)
		if len(m) > 1 {
			return m[1]
		}
	}
	return ""
}
func sourceEqual(a, b protocol.JSONObject, block bool) bool {
	ak, bk := a.String("source"), b.String("source")
	constraints := func() bool {
		for _, key := range []string{"ref", "path"} {
			left, right := a.String(key), b.String(key)
			if block && right == "" {
				continue
			}
			if left != right {
				return false
			}
		}
		return true
	}
	if ak != bk {
		if block && ak == "git" && bk == "github" {
			return gitHubRepo(a.String("url")) == b.String("repo") && constraints()
		}
		if block && ak == "github" && bk == "git" {
			return a.String("repo") == gitHubRepo(b.String("url")) && constraints()
		}
		return false
	}
	switch ak {
	case "github":
		return a.String("repo") == b.String("repo") && constraints()
	case "git":
		return a.String("url") == b.String("url") && constraints()
	case "url":
		return a.String("url") == b.String("url")
	case "npm":
		return a.String("package") == b.String("package")
	case "file", "directory":
		return a.String("path") == b.String("path")
	case "settings":
		if a.String("name") != b.String("name") {
			return false
		}
		if block {
			return true
		}
		var x, y any
		_ = json.Unmarshal(a.Get("plugins"), &x)
		_ = json.Unmarshal(b.Get("plugins"), &y)
		return reflect.DeepEqual(x, y)
	}
	return false
}
func (p Policy) Blocked(source protocol.JSONObject) bool {
	for _, blocked := range p.Blocklist {
		if sourceEqual(source, blocked, true) {
			return true
		}
	}
	return false
}
func (p Policy) Allowed(source protocol.JSONObject) bool {
	if p.Blocked(source) {
		return false
	}
	if !p.HasAllowlist {
		return true
	}
	for _, allowed := range p.Allowlist {
		switch allowed.String("source") {
		case "hostPattern":
			host := sourceHost(source)
			if host != "" && patternMatches(allowed.String("hostPattern"), host) {
				return true
			}
		case "pathPattern":
			kind := source.String("source")
			if (kind == "file" || kind == "directory") && patternMatches(allowed.String("pathPattern"), source.String("path")) {
				return true
			}
		default:
			if sourceEqual(source, allowed, false) {
				return true
			}
		}
	}
	return false
}
func FormatSource(source protocol.JSONObject) string {
	suffix := ""
	if ref := source.String("ref"); ref != "" {
		suffix = "@" + ref
	}
	switch source.String("source") {
	case "github":
		return "github:" + source.String("repo") + suffix
	case "url":
		return source.String("url")
	case "git":
		return "git:" + source.String("url") + suffix
	case "npm":
		return "npm:" + source.String("package")
	case "file":
		return "file:" + source.String("path")
	case "directory":
		return "dir:" + source.String("path")
	case "hostPattern":
		return "hostPattern:" + source.String("hostPattern")
	case "pathPattern":
		return "pathPattern:" + source.String("pathPattern")
	case "settings":
		var plugins []any
		_ = json.Unmarshal(source.Get("plugins"), &plugins)
		suffix := "plugins"
		if len(plugins) == 1 {
			suffix = "plugin"
		}
		return "settings:" + source.String("name") + " (" + strings.TrimSpace(string(raw(len(plugins)))) + " " + suffix + ")"
	}
	return "unknown source"
}
