package settingswatch

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

var Sources = []string{"userSettings", "projectSettings", "localSettings", "flagSettings", "policySettings"}

type SourceFile struct{ Source, Path string }
type Targets struct {
	Directories     []string
	Files           []SourceFile
	DropInDirectory string
}

// Collect includes all possible setting paths in a selected directory, but
// selects that directory only if at least one of its setting files exists.
// This is Source getWatchTargets, not recursive project-file discovery.
func Collect(files []SourceFile, dropIn string) (Targets, error) {
	result := Targets{Directories: []string{}, Files: []SourceFile{}}
	byDirectory := map[string][]SourceFile{}
	ordered := []string{}
	existing := map[string]bool{}
	for _, file := range files {
		if file.Source == "flagSettings" || file.Path == "" {
			continue
		}
		if !filepath.IsAbs(file.Path) {
			return result, errors.New("settings watcher requires absolute source paths")
		}
		directory := filepath.Dir(file.Path)
		if _, ok := byDirectory[directory]; !ok {
			ordered = append(ordered, directory)
		}
		byDirectory[directory] = append(byDirectory[directory], file)
		if info, err := os.Stat(file.Path); err == nil && info.Mode().IsRegular() {
			existing[directory] = true
		}
	}
	for _, directory := range ordered {
		if existing[directory] {
			result.Directories = append(result.Directories, directory)
			result.Files = append(result.Files, byDirectory[directory]...)
		}
	}
	if dropIn != "" {
		if !filepath.IsAbs(dropIn) {
			return result, errors.New("settings watcher requires absolute managed drop-in directory")
		}
		if info, err := os.Stat(dropIn); err == nil && info.IsDir() {
			if !existing[dropIn] {
				result.Directories = append(result.Directories, dropIn)
			}
			result.DropInDirectory = dropIn
		}
	}
	return result, nil
}

func ConfigSource(source string) string {
	switch source {
	case "userSettings":
		return "user_settings"
	case "projectSettings":
		return "project_settings"
	case "localSettings":
		return "local_settings"
	default:
		return "policy_settings"
	}
}
func SourceForPath(path string, files []SourceFile, dropIn string) string {
	path = filepath.Clean(path)
	if dropIn != "" && strings.HasPrefix(path, dropIn+string(filepath.Separator)) {
		return "policySettings"
	}
	for _, source := range Sources {
		for _, file := range files {
			if file.Source == source && file.Path == path {
				return source
			}
		}
	}
	return ""
}

func (target Targets) accepts(path string) bool {
	for _, part := range strings.Split(path, string(filepath.Separator)) {
		if part == ".git" {
			return false
		}
	}
	for _, file := range target.Files {
		if file.Path == path {
			return true
		}
	}
	return target.DropInDirectory != "" && filepath.Dir(path) == target.DropInDirectory && strings.HasSuffix(path, ".json")
}
