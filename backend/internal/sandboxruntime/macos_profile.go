package sandboxruntime

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"path/filepath"
	"strings"
)

// Only declarative static Seatbelt lines from the installed Apache-2.0 package.
// Branches, path resolution and native execution are implemented below in Go.
//
//go:embed assets/macos-profile-base.json
var macProfileData []byte

type ReadRestriction struct {
	DenyOnly        []string `json:"denyOnly"`
	AllowWithinDeny []string `json:"allowWithinDeny"`
}
type WriteRestriction struct {
	AllowOnly       []string `json:"allowOnly"`
	DenyWithinAllow []string `json:"denyWithinAllow"`
}
type MacProfileOptions struct {
	Paths                                                                                          PathContext
	Read                                                                                           *ReadRestriction
	Write                                                                                          *WriteRestriction
	HTTPProxyPort, SOCKSProxyPort                                                                  *float64
	NeedsNetworkRestriction                                                                        bool
	AllowUnixSockets                                                                               []string
	AllowAllUnixSockets, AllowLocalBinding, AllowPty, AllowGitConfig, EnableWeakerNetworkIsolation bool
	LogTag                                                                                         string
}

func jsonQuote(value string) string {
	var b bytes.Buffer
	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
	return strings.TrimSuffix(b.String(), "\n")
}
func ancestors(value string) []string {
	out := []string{}
	current := filepath.Dir(value)
	for current != "/" && current != "." {
		out = append(out, current)
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return out
}
func moveRules(patterns []string, ctx PathContext, tag string) []string {
	out := []string{}
	literal := func(path string) {
		out = append(out, "(deny file-write-unlink", "  (literal "+jsonQuote(path)+")", "  (with message \""+tag+"\"))")
	}
	for _, pattern := range patterns {
		normalized := NormalizePath(pattern, ctx)
		if ContainsGlob(normalized) {
			out = append(out, "(deny file-write-unlink", "  (regex "+jsonQuote(GlobRegex(normalized))+")", "  (with message \""+tag+"\"))")
			prefix := normalized[:strings.IndexAny(normalized, "*?[]")]
			if prefix != "" && prefix != "/" {
				base := filepath.Dir(prefix)
				if strings.HasSuffix(prefix, "/") {
					base = strings.TrimSuffix(prefix, "/")
				}
				literal(base)
				for _, ancestor := range ancestors(base) {
					literal(ancestor)
				}
			}
		} else {
			out = append(out, "(deny file-write-unlink", "  (subpath "+jsonQuote(normalized)+")", "  (with message \""+tag+"\"))")
			for _, ancestor := range ancestors(normalized) {
				literal(ancestor)
			}
		}
	}
	return out
}
func pathRules(action, operation string, patterns []string, ctx PathContext, tag string) []string {
	out := []string{}
	for _, pattern := range patterns {
		value := NormalizePath(pattern, ctx)
		kind := "subpath"
		if ContainsGlob(value) {
			kind = "regex"
			value = GlobRegex(value)
		}
		out = append(out, "("+action+" "+operation, "  ("+kind+" "+jsonQuote(value)+")", "  (with message \""+tag+"\"))")
	}
	return out
}
func readRules(value *ReadRestriction, ctx PathContext, tag string) []string {
	out := []string{"(allow file-read*)"}
	if value == nil {
		return out
	}
	out = append(out, pathRules("deny", "file-read*", value.DenyOnly, ctx, tag)...)
	for _, pattern := range value.DenyOnly {
		if NormalizePath(pattern, ctx) == "/" {
			out = append(out, "(allow file-read* (literal \"/\"))")
			break
		}
	}
	out = append(out, pathRules("allow", "file-read*", value.AllowWithinDeny, ctx, tag)...)
	if len(value.DenyOnly) > 0 {
		out = append(out, "(allow file-read-metadata", "  (vnode-type DIRECTORY))")
	}
	return append(out, moveRules(value.DenyOnly, ctx, tag)...)
}
func writeRules(value *WriteRestriction, ctx PathContext, tag string, allowGitConfig bool) []string {
	if value == nil {
		return []string{"(allow file-write*)"}
	}
	out := pathRules("allow", "file-write*", value.AllowOnly, ctx, tag)
	denied := append(append([]string{}, value.DenyWithinAllow...), MandatoryMacDenyPaths(ctx.CWD, allowGitConfig)...)
	out = append(out, pathRules("deny", "file-write*", denied, ctx, tag)...)
	return append(out, moveRules(denied, ctx, tag)...)
}
func MacProfile(in MacProfileOptions) (string, error) {
	in.Paths.CWD = resolvedWorkingDirectory(in.Paths)
	var declarations struct{ Prefix, Suffix []string }
	if e := json.Unmarshal(macProfileData, &declarations); e != nil {
		return "", e
	}
	lines := []string{}
	add := func(values ...string) {
		for _, line := range values {
			lines = append(lines, strings.ReplaceAll(line, "{{LOG_TAG}}", in.LogTag))
		}
	}
	add(declarations.Prefix...)
	if in.EnableWeakerNetworkIsolation {
		add("; trustd.agent - needed for Go TLS certificate verification (weaker network isolation)", "(allow mach-lookup (global-name \"com.apple.trustd.agent\"))")
	}
	add(declarations.Suffix...)
	add("; Network")
	if !in.NeedsNetworkRestriction {
		add("(allow network*)")
	} else {
		if in.AllowLocalBinding {
			add("(allow network-bind (local ip \"*:*\"))", "(allow network-inbound (local ip \"*:*\"))", "(allow network-outbound (local ip \"*:*\"))")
		}
		if in.AllowAllUnixSockets {
			add("(allow system-socket (socket-domain AF_UNIX))", "(allow network-bind (local unix-socket (path-regex #\"^/\")))", "(allow network-outbound (remote unix-socket (path-regex #\"^/\")))")
		} else if len(in.AllowUnixSockets) > 0 {
			add("(allow system-socket (socket-domain AF_UNIX))")
			for _, path := range in.AllowUnixSockets {
				quoted := jsonQuote(NormalizePath(path, in.Paths))
				add("(allow network-bind (local unix-socket (subpath "+quoted+")))", "(allow network-outbound (remote unix-socket (subpath "+quoted+")))")
			}
		}
		for _, port := range []*float64{in.HTTPProxyPort, in.SOCKSProxyPort} {
			if port != nil {
				value := numberString(*port)
				add("(allow network-bind (local ip \"localhost:"+value+"\"))", "(allow network-inbound (local ip \"localhost:"+value+"\"))", "(allow network-outbound (remote ip \"localhost:"+value+"\"))")
			}
		}
	}
	add("", "; File read")
	add(readRules(in.Read, in.Paths, in.LogTag)...)
	add("", "; File write")
	add(writeRules(in.Write, in.Paths, in.LogTag, in.AllowGitConfig)...)
	if in.AllowPty {
		add("", "; Pseudo-terminal (pty) support", "(allow pseudo-tty)", "(allow file-ioctl", "  (literal \"/dev/ptmx\")", "  (regex #\"^/dev/ttys\")", ")", "(allow file-read* file-write*", "  (literal \"/dev/ptmx\")", "  (regex #\"^/dev/ttys\")", ")")
	}
	return strings.Join(lines, "\n"), nil
}
