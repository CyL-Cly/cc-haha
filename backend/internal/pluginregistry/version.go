package pluginregistry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/gitbranch"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/text/unicode/norm"
)

func CalculateVersion(source json.RawMessage, manifest protocol.JSONObject, installPath, provided, resolvedSHA string) string {
	if version := manifest.String("version"); version != "" {
		return version
	}
	if provided != "" {
		return provided
	}
	if resolvedSHA != "" {
		short := resolvedSHA
		if len(short) > 12 {
			short = short[:12]
		}
		if s := object(source); s.String("source") == "git-subdir" {
			path := strings.TrimRight(strings.TrimPrefix(strings.ReplaceAll(s.String("path"), `\`, "/"), "./"), "/")
			hash := sha256.Sum256([]byte(path))
			return short + "-" + hex.EncodeToString(hash[:])[:8]
		}
		return short
	}
	if installPath != "" {
		if sha := ReadGitSHA(installPath); sha != "" {
			if len(sha) > 12 {
				sha = sha[:12]
			}
			return sha
		}
	}
	return "unknown"
}

var validGitSHA = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

func readGitText(path string) (string, bool) {
	b, err := os.ReadFile(path)
	return strings.TrimSpace(string(protocol.DecodeJavaScriptUTF8(b))), err == nil
}

// ReadGitSHA follows the original filesystem reader, including worktree
// pointers, loose/symbolic/packed refs and shared commondir. No git process is
// launched solely to calculate a cache key.
func ReadGitSHA(start string) string {
	dir, err := filepath.Abs(start)
	if err != nil {
		return ""
	}
	gitDir := ""
	for {
		path := filepath.Join(dir, ".git")
		if info, err := os.Stat(path); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
			gitDir = filepath.Join(norm.NFC.String(dir), ".git")
			if info.Mode().IsRegular() {
				if value, ok := readGitText(gitDir); ok && strings.HasPrefix(value, "gitdir:") {
					gitDir = resolveRelative(dir, strings.TrimSpace(strings.TrimPrefix(value, "gitdir:")))
				}
			}
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
	var resolveRef func(string, string, map[string]bool) string
	resolveRef = func(base, ref string, seen map[string]bool) string {
		if !gitbranch.SafeRef(ref) || seen[base+"\x00"+ref] {
			return ""
		}
		seen[base+"\x00"+ref] = true
		readIn := func(dir string) string {
			if text, ok := readGitText(filepath.Join(dir, ref)); ok {
				if strings.HasPrefix(text, "ref:") {
					return resolveRef(dir, strings.TrimSpace(strings.TrimPrefix(text, "ref:")), seen)
				}
				if validGitSHA.MatchString(text) {
					return text
				}
				return ""
			}
			if data, err := os.ReadFile(filepath.Join(dir, "packed-refs")); err == nil {
				for _, line := range strings.Split(string(protocol.DecodeJavaScriptUTF8(data)), "\n") {
					if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "^") {
						continue
					}
					sha, name, ok := strings.Cut(line, " ")
					if ok && name == ref {
						if validGitSHA.MatchString(sha) {
							return sha
						}
						return ""
					}
				}
			}
			return ""
		}
		if value := readIn(base); value != "" {
			return value
		}
		if common, ok := readGitText(filepath.Join(base, "commondir")); ok {
			path := resolveRelative(base, common)
			if path != base {
				return readIn(path)
			}
		}
		return ""
	}
	text, ok := readGitText(filepath.Join(gitDir, "HEAD"))
	if !ok {
		return ""
	}
	if strings.HasPrefix(text, "ref:") {
		return resolveRef(gitDir, strings.TrimSpace(strings.TrimPrefix(text, "ref:")), map[string]bool{})
	}
	if validGitSHA.MatchString(text) {
		return text
	}
	return ""
}
func resolveRelative(base, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(base, path)
}
