package heapdump

import (
	"path/filepath"
	"strings"
)

// Desktop preserves the original OS directory selection, including the WSL
// mounted Windows home and Linux's home fallback when Desktop is absent.
func Desktop(platform, home, userProfile string, exists func(string) bool, directories func(string) ([]string, error)) string {
	if platform == "darwin" || platform == "macos" {
		return filepath.Join(home, "Desktop")
	}
	if platform == "windows" {
		if userProfile != "" {
			profile := strings.ReplaceAll(userProfile, "\\", "/")
			if len(profile) >= 2 && profile[0] >= 'A' && profile[0] <= 'Z' && profile[1] == ':' {
				profile = profile[2:]
			}
			candidate := filepath.Join("/mnt/c", profile, "Desktop")
			if exists(candidate) {
				return candidate
			}
		}
		if users, err := directories("/mnt/c/Users"); err == nil {
			for _, user := range users {
				if user == "Public" || user == "Default" || user == "Default User" || user == "All Users" {
					continue
				}
				candidate := filepath.Join("/mnt/c/Users", user, "Desktop")
				if exists(candidate) {
					return candidate
				}
			}
		}
	}
	candidate := filepath.Join(home, "Desktop")
	if exists(candidate) {
		return candidate
	}
	return home
}
