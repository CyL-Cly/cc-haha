package opentargets

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func marshalTarget(t Target) ([]byte, error) {
	type wire Target
	if t.Kind == "application" {
		return json.Marshal(struct {
			wire
			BundleID *string `json:"bundleId"`
		}{wire(t), t.BundleID})
	}
	return json.Marshal(wire(t))
}
func jsonString(raw json.RawMessage) (string, bool) {
	units, err := protocol.DecodeJavaScriptJSONString(raw)
	if err != nil {
		return "", false
	}
	return string(utf16.Decode(units)), true
}
func trimJS(value string) string {
	return strings.TrimFunc(value, func(r rune) bool {
		return r >= 9 && r <= 13 || r == ' ' || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000 || r == 0xfeff
	})
}
func encodeURIComponent(value string) string {
	var out strings.Builder
	for _, b := range []byte(value) {
		if b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || strings.ContainsRune("-_.!~*'()", rune(b)) {
			out.WriteByte(b)
		} else {
			fmt.Fprintf(&out, "%%%02X", b)
		}
	}
	return out.String()
}
func iconKey(platform, id string, size int) string {
	return fmt.Sprintf("%s:%s:%d", platform, id, size)
}
func hostDir(value string) string { return filepath.Dir(value) }
func nodeExt(base string) string {
	index := strings.LastIndex(base, ".")
	if index <= 0 || base == ".." {
		return ""
	}
	return base[index:]
}
func hostExt(value string) string { return nodeExt(filepath.Base(value)) }
func winExt(value string) string {
	value = strings.ReplaceAll(value, "\\", "/")
	return nodeExt(filepath.Base(value))
}
func winDir(value string) string {
	v := strings.ReplaceAll(value, "/", "\\")
	if len(v) >= 3 && v[1] == ':' && v[2] == '\\' {
		for len(v) > 3 && strings.HasSuffix(v, "\\") {
			v = v[:len(v)-1]
		}
		if i := strings.LastIndex(v, "\\"); i >= 2 {
			if i == 2 {
				return v[:3]
			}
			return v[:i]
		}
	}
	if strings.HasPrefix(v, "\\\\") {
		parts := strings.Split(strings.Trim(v, "\\"), "\\")
		if len(parts) <= 2 {
			return "\\\\" + strings.Join(parts, "\\") + "\\"
		}
	}
	for len(v) > 1 && strings.HasSuffix(v, "\\") {
		v = v[:len(v)-1]
	}
	if i := strings.LastIndex(v, "\\"); i >= 0 {
		if i == 0 {
			return "\\"
		}
		return v[:i]
	}
	if len(v) >= 2 && v[1] == ':' {
		return v[:2]
	}
	return "."
}
func winJoin(dir, name string) string {
	return strings.TrimRight(dir, "\\/") + "\\" + strings.ReplaceAll(name, "/", "\\")
}
func blockedExtension(value string) bool {
	switch strings.ToLower(hostExt(value)) {
	case ".app", ".bat", ".cmd", ".com", ".exe", ".msi", ".ps1", ".scr", ".sh":
		return true
	}
	return false
}
func methodError(method string) error {
	return apiError(405, "Method "+method+" not allowed", "METHOD_NOT_ALLOWED")
}
func (s *Service) HandleHTTP(w http.ResponseWriter, r *http.Request, segments []string) {
	action := ""
	if len(segments) > 2 {
		action = segments[2]
	}
	var result any
	var err error
	switch action {
	case "":
		if r.Method != http.MethodGet {
			err = methodError(r.Method)
			break
		}
		if path := r.URL.Query().Get("path"); path != "" {
			result, err = s.ListForPath(r.Context(), path)
		} else {
			result, err = s.List(r.Context(), false)
		}
	case "open":
		if r.Method != http.MethodPost {
			err = methodError(r.Method)
			break
		}
		var body map[string]json.RawMessage
		body, err = parseBody(r)
		if err != nil {
			break
		}
		id, _ := jsonString(body["targetId"])
		id = trimJS(id)
		path, _ := jsonString(body["path"])
		if id == "" {
			err = apiError(400, "Missing or invalid \"targetId\" in request body", "BAD_REQUEST")
			break
		}
		if path == "" || trimJS(path) == "" {
			err = apiError(400, "Missing or invalid \"path\" in request body", "BAD_REQUEST")
			break
		}
		result, err = s.Open(r.Context(), id, path)
	case "icons":
		if r.Method != http.MethodGet {
			err = methodError(r.Method)
			break
		}
		id := ""
		if len(segments) > 3 {
			id, err = url.PathUnescape(segments[3])
			if err != nil {
				break
			}
			if !utf8.ValidString(id) {
				err = errors.New("URI malformed")
				break
			}
			id = trimJS(id)
		}
		if id == "" {
			err = apiError(400, "Missing open target icon id", "BAD_REQUEST")
			break
		}
		var icon Icon
		icon, err = s.GetIcon(r.Context(), id, 64)
		if err == nil {
			w.Header().Set("Cache-Control", "private, max-age=86400")
			w.Header().Set("Content-Type", icon.ContentType)
			w.WriteHeader(200)
			_, _ = w.Write(icon.Data)
			return
		}
	default:
		err = apiError(404, "Unknown open-targets endpoint: "+action, "NOT_FOUND")
	}
	status := 200
	if err != nil {
		var api *Error
		if errors.As(err, &api) {
			status = api.Status
			result = map[string]string{"error": api.Code, "message": api.Message}
		} else {
			status = 500
			result = map[string]string{"error": "INTERNAL_ERROR", "message": "An unexpected error occurred"}
		}
	}
	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(result)
}
func parseBody(r *http.Request) (map[string]json.RawMessage, error) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, apiError(400, "Invalid JSON body", "BAD_REQUEST")
	}
	raw = bytesWithoutBOM(protocol.DecodeJavaScriptUTF8(raw))
	if !json.Valid(raw) {
		return nil, apiError(400, "Invalid JSON body", "BAD_REQUEST")
	}
	body := map[string]json.RawMessage{}
	_ = json.Unmarshal(raw, &body)
	return body, nil
}
func bytesWithoutBOM(raw []byte) []byte {
	if strings.HasPrefix(string(raw), "\ufeff") {
		return raw[3:]
	}
	return raw
}
