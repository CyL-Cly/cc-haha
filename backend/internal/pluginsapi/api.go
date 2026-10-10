package pluginsapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

func trimJS(s string) string {
	return strings.TrimFunc(s, javaScriptWhitespace)
}
func javaScriptWhitespace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}
func (s *Service) Handle(ctx context.Context, method string, u *url.URL, parts []string, body []byte) (int, any, error) {
	sub := ""
	if len(parts) > 2 {
		sub = parts[2]
	}
	cwd := u.Query().Get("cwd")
	if method == "GET" && sub == "" {
		value, err := s.List(ctx, cwd)
		return 200, value, err
	}
	if method == "GET" && sub == "detail" {
		id := u.Query().Get("id")
		if id == "" {
			return 0, nil, bad("Missing required \"id\" query parameter")
		}
		value, err := s.Detail(ctx, id, cwd)
		return 200, rec(field("detail", value)), err
	}
	if method == "POST" && sub == "reload" {
		value, err := s.Reload(ctx, cwd)
		if err == nil && u.Query().Get("sessionId") != "" {
			var session any
			session, err = s.options.ReloadSession(ctx, u.Query().Get("sessionId"))
			if err == nil {
				value.Set("session", raw(session))
			}
		}
		return 200, value, err
	}
	if method == "POST" && sub != "" {
		if !json.Valid(body) {
			return 0, nil, bad("Invalid JSON body")
		}
		body = bytes.TrimSpace(body)
		b := obj(body)
		if len(body) == 0 || body[0] != '{' {
			return 0, nil, bad("JSON body must be an object")
		}
		id := trimJS(b.String("id"))
		if id == "" || len(utf16.Encode([]rune(id))) > 512 {
			return 0, nil, bad("Missing or invalid \"id\" in request body")
		}
		for _, key := range properties(b) {
			if key != "id" && key != "scope" && key != "cwd" && !(sub == "uninstall" && key == "keepData") {
				if key != "" {
					return 0, nil, bad("Unknown request field: \"" + key + "\"")
				}
				break
			}
		}
		cwd = ""
		v := b.Get("cwd")
		if len(v) > 0 && string(v) != "null" {
			cwd = b.String("cwd")
			if len(v) == 0 || v[0] != '"' || cwd == "" || len(utf16.Encode([]rune(cwd))) > 4096 {
				return 0, nil, bad("\"cwd\" must be a non-empty absolute directory path")
			}
			if !filepath.IsAbs(cwd) {
				return 0, nil, bad("\"cwd\" must be an absolute directory path")
			}
			cwd = filepath.Clean(cwd)
			info, err := os.Stat(cwd)
			if err != nil || !info.IsDir() {
				return 0, nil, bad("\"cwd\" must reference an existing directory")
			}
		}
		if sub != "enable" && sub != "disable" && sub != "update" && sub != "uninstall" {
			return 0, nil, missing("Unknown plugins endpoint: " + sub)
		}
		scope := ""
		v = b.Get("scope")
		if len(v) > 0 && string(v) != "null" {
			scope = b.String("scope")
			if scope != "user" && scope != "project" && scope != "local" && !(scope == "managed" && sub == "update") {
				suffix := ""
				if sub == "update" {
					suffix = ", managed"
				}
				return 0, nil, bad("Invalid \"scope\". Expected one of: user, project, local" + suffix)
			}
		}
		if sub == "enable" || sub == "disable" {
			if err := requireRoot(scope, cwd); err != nil {
				return 0, nil, err
			}
			value, err := s.SetEnabled(ctx, id, sub == "enable", scope, cwd)
			return 200, value, err
		}
		if sub == "update" {
			if scope == "" {
				return 0, nil, bad("Plugin update requires a scope")
			}
			if err := requireRoot(scope, cwd); err != nil {
				return 0, nil, err
			}
			value, err := s.Update(ctx, id, scope, cwd)
			return 200, value, err
		}
		v = b.Get("keepData")
		if v != nil && string(v) != "true" && string(v) != "false" {
			return 0, nil, bad("\"keepData\" must be a boolean")
		}
		if scope == "" {
			return 0, nil, bad("Plugin uninstall requires a scope")
		}
		if err := requireRoot(scope, cwd); err != nil {
			return 0, nil, err
		}
		value, err := s.Uninstall(ctx, id, scope, cwd, string(v) == "true")
		return 200, value, err
	}
	endpoint := "/api/plugins"
	if sub != "" {
		endpoint += "/" + sub
	}
	return 0, nil, &APIError{StatusCode: 405, Code: "METHOD_NOT_ALLOWED", Message: "Method " + method + " not allowed on " + endpoint}
}
