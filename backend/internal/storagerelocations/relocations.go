// Package storagerelocations maps only app-owned attachment paths after a data
// directory move. It never rewrites project paths, transcripts or metadata.
package storagerelocations

import (
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/text/unicode/norm"
)

type Metadata struct {
	Version       int
	PreviousRoots []string
	// Preserve unrecognized fields for callers that must forward the document.
	Document map[string]json.RawMessage
}

var ManagedPrefixes = []string{"uploads", "image-cache", "im-downloads", "cc-haha/generated-images"}

func windowsPlatform(platform string) bool { return platform == "windows" || platform == "win32" }

func isAbsolute(input, platform string) bool {
	if windowsPlatform(platform) {
		input = strings.ReplaceAll(input, "/", `\`)
		return strings.HasPrefix(input, `\`) || len(input) >= 3 && input[1] == ':' && input[2] == '\\' && (input[0] >= 'a' && input[0] <= 'z' || input[0] >= 'A' && input[0] <= 'Z')
	}
	return strings.HasPrefix(input, "/")
}

func Read(configDir string, platform string) (Metadata, error) {
	data, err := os.ReadFile(filepath.Join(configDir, "cc-haha", "storage-relocations.json"))
	if errors.Is(err, os.ErrNotExist) {
		return Metadata{Version: 1, PreviousRoots: []string{}, Document: map[string]json.RawMessage{"version": json.RawMessage("1"), "previousRoots": json.RawMessage("[]")}}, nil
	}
	if err != nil {
		return Metadata{}, errors.New("Cannot read storage relocation metadata")
	}
	// MAIN parses the stored JSON inside its read error boundary. Invalid
	// syntax is a read failure; a valid JSON value with the wrong shape is a
	// distinct metadata validation failure below.
	if !json.Valid(data) {
		return Metadata{}, errors.New("Cannot read storage relocation metadata")
	}
	var document map[string]json.RawMessage
	if json.Unmarshal(data, &document) != nil || document == nil {
		return Metadata{}, errors.New("Invalid storage relocation metadata")
	}
	var version float64
	var roots []string
	if json.Unmarshal(document["version"], &version) != nil || version != 1 || string(document["previousRoots"]) == "null" || json.Unmarshal(document["previousRoots"], &roots) != nil || roots == nil {
		return Metadata{}, errors.New("Unsupported or invalid storage relocation metadata")
	}
	for _, root := range roots {
		if root == "" || strings.ContainsRune(root, 0) || !isAbsolute(root, platform) {
			return Metadata{}, errors.New("Unsupported or invalid storage relocation metadata")
		}
	}
	return Metadata{Version: 1, PreviousRoots: roots, Document: document}, nil
}

func windowsClean(input string) string {
	input = strings.ReplaceAll(input, "/", `\`)
	volume, rest, rooted := "", input, false
	if len(input) >= 2 && input[1] == ':' {
		volume, rest = input[:2], input[2:]
		rooted = strings.HasPrefix(rest, `\`)
	} else if strings.HasPrefix(input, `\\`) && len(input) > 2 && input[2] != '\\' {
		parts := strings.Split(strings.TrimLeft(input, `\`), `\`)
		if len(parts) >= 2 && parts[0] != "" && parts[1] != "" {
			volume = `\\` + parts[0] + `\` + parts[1]
			rest = strings.Join(parts[2:], `\`)
			rooted = true
		} else {
			rooted = true
		}
	} else {
		rooted = strings.HasPrefix(input, `\`)
	}
	clean := strings.ReplaceAll(rest, `\`, "/")
	if rooted {
		clean = "/" + strings.TrimLeft(clean, "/")
	}
	clean = path.Clean(clean)
	if rooted && clean == "/" {
		return volume + `\`
	}
	return volume + strings.ReplaceAll(clean, "/", `\`)
}

// Map mirrors MAIN's explicit platform path boundary, including case-insensitive
// Windows drives/UNC and NFC, independently of the current test host platform.
func Map(inputPath, configDir string, previousRoots []string, platform string) string {
	if strings.ContainsRune(inputPath, 0) || !isAbsolute(inputPath, platform) {
		return inputPath
	}
	sep := "/"
	clean := path.Clean
	if windowsPlatform(platform) {
		sep = `\`
		clean = windowsClean
	}
	normalized := norm.NFC.String(clean(inputPath))
	for _, root := range previousRoots {
		root = norm.NFC.String(clean(root))
		prefix := strings.TrimRight(root, sep) + sep
		matching, prefixMatch := normalized, prefix
		if windowsPlatform(platform) {
			matching = strings.ToLower(matching)
			prefixMatch = strings.ToLower(prefixMatch)
		}
		if !strings.HasPrefix(matching, prefixMatch) || len(normalized) <= len(prefix) {
			continue
		}
		relative := normalized[len(prefix):]
		comparable := strings.ReplaceAll(relative, `\`, "/")
		if windowsPlatform(platform) {
			comparable = strings.ToLower(comparable)
		}
		for _, managed := range ManagedPrefixes {
			if strings.HasPrefix(comparable, managed+"/") {
				return norm.NFC.String(clean(strings.TrimRight(configDir, sep) + sep + relative))
			}
		}
	}
	return inputPath
}

func Resolve(inputPath, configDir string) (string, error) {
	return ResolveForPlatform(inputPath, configDir, runtime.GOOS)
}
func ResolveForPlatform(inputPath, configDir, platform string) (string, error) {
	metadata, err := Read(configDir, platform)
	if err != nil {
		return "", err
	}
	return Map(inputPath, configDir, metadata.PreviousRoots, platform), nil
}
