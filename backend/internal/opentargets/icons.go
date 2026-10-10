package opentargets

import (
	"context"
	"path"
	"regexp"
	"strings"
)

func (s *Service) bundleIcon(ctx context.Context, app string, names []string) string {
	resources := path.Join(app, "Contents", "Resources")
	value := s.primitives.ReadPlistValue(ctx, path.Join(app, "Contents", "Info.plist"), "CFBundleIconFile")
	candidates := []string{}
	if value != nil && *value != "" {
		name := trimJS(*value)
		if nodeExt(path.Base(name)) == "" {
			name += ".icns"
		}
		candidates = append(candidates, name)
	}
	for _, name := range names {
		candidates = append(candidates, name+".icns")
	}
	for _, name := range candidates {
		p := path.Join(resources, name)
		if s.primitives.PathExists(p) {
			return p
		}
	}
	for _, name := range s.primitives.ReadDirNames(resources) {
		if strings.HasSuffix(name, ".icns") && !strings.Contains(strings.ToLower(name), "document") {
			p := path.Join(resources, name)
			if s.primitives.PathExists(p) {
				return p
			}
			break
		}
	}
	return ""
}
func (s *Service) resolveIcon(ctx context.Context, d definition) string {
	p := s.options.Platform
	if !d.supported(p) {
		return ""
	}
	if p == "win32" && d.fallback && d.id == "explorer" {
		if command, err := s.primitives.ResolveCommand(ctx, "explorer.exe"); err == nil && command != "" {
			return command
		}
	}
	for _, icon := range d.icons[p] {
		if s.primitives.PathExists(icon) {
			return icon
		}
	}
	switch p {
	case "darwin":
		for _, app := range d.apps[p] {
			if s.primitives.PathExists(app) {
				if icon := s.bundleIcon(ctx, app, []string{d.label, d.icon}); icon != "" {
					return icon
				}
			}
		}
	case "win32":
		return s.windowsApplication(ctx, d)
	case "linux":
		if !d.fallback {
			for _, entry := range s.linuxDesktopEntries(d) {
				if entry.icon != nil && *entry.icon != "" {
					if icon := s.linuxIcon(*entry.icon); icon != "" {
						return icon
					}
				}
			}
		}
		if d.fallback && d.kind == "file_manager" {
			for _, name := range []string{"folder", "system-file-manager", "org.gnome.Nautilus"} {
				if icon := s.linuxIcon(name); icon != "" {
					return icon
				}
			}
		}
	}
	return ""
}

type desktopEntry struct {
	file             string
	name, exec, icon *string
}

var desktopLocale = regexp.MustCompile(`\[.*\]$`)
var desktopSearch = regexp.MustCompile(`[^a-z0-9]+`)
var desktopCode = regexp.MustCompile(`%[a-zA-Z]`)
var desktopTokens = regexp.MustCompile(`"([^"]+)"|'([^']+)'|(\S+)`)

func parseDesktop(file, text string) *desktopEntry {
	inEntry := false
	values := map[string]string{}
	for _, raw := range strings.Split(text, "\n") {
		line := trimJS(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			inEntry = line == "[Desktop Entry]"
			continue
		}
		if !inEntry {
			continue
		}
		index := strings.Index(line, "=")
		if index <= 0 {
			continue
		}
		key := desktopLocale.ReplaceAllString(trimJS(line[:index]), "")
		switch key {
		case "Name", "Exec", "Icon":
			values[key] = trimJS(line[index+1:])
		}
	}
	_, hasExec := values["Exec"]
	_, hasIcon := values["Icon"]
	if !hasExec && !hasIcon {
		return nil
	}
	entry := &desktopEntry{file: file}
	if value, exists := values["Name"]; exists {
		entry.name = &value
	}
	if value, exists := values["Exec"]; exists {
		entry.exec = &value
	}
	if value, exists := values["Icon"]; exists {
		entry.icon = &value
	}
	return entry
}
func searchText(value string) string {
	return desktopSearch.ReplaceAllString(strings.ToLower(value), "")
}
func desktopCommand(value string) string {
	for _, token := range desktopTokens.FindAllString(desktopCode.ReplaceAllString(value, ""), -1) {
		if strings.HasPrefix(token, "'") || strings.HasPrefix(token, "\"") {
			token = token[1:]
		}
		if strings.HasSuffix(token, "'") || strings.HasSuffix(token, "\"") {
			token = token[:len(token)-1]
		}
		if token == "" || strings.Contains(token, "=") {
			continue
		}
		command := strings.ToLower(path.Base(token))
		if command == "env" {
			continue
		}
		return command
	}
	return ""
}
func matchesDesktop(entry *desktopEntry, file string, d definition) bool {
	commands := map[string]bool{}
	needles := []string{d.id, d.icon, d.label}
	for _, command := range d.commands["linux"] {
		name := strings.ToLower(path.Base(command))
		commands[name] = true
		needles = append(needles, name)
	}
	file = searchText(file)
	name := ""
	if entry.name != nil {
		name = searchText(*entry.name)
	}
	for _, needle := range needles {
		needle = searchText(needle)
		if needle != "" && (strings.Contains(file, needle) || strings.Contains(name, needle)) {
			return true
		}
	}
	if entry.exec != nil {
		return commands[desktopCommand(*entry.exec)]
	}
	return false
}
func (s *Service) linuxDesktopEntries(d definition) []desktopEntry {
	result := []desktopEntry{}
	for _, dir := range []string{"/usr/share/applications", "/usr/local/share/applications", path.Join(s.options.Home, ".local", "share", "applications"), "/var/lib/flatpak/exports/share/applications", path.Join(s.options.Home, ".local", "share", "flatpak", "exports", "share", "applications")} {
		for _, name := range s.primitives.ReadDirNames(dir) {
			if !strings.HasSuffix(name, ".desktop") {
				continue
			}
			file := path.Join(dir, name)
			text := s.primitives.ReadTextFile(file)
			if text == nil || *text == "" {
				continue
			}
			entry := parseDesktop(file, *text)
			if entry != nil && matchesDesktop(entry, name, d) {
				result = append(result, *entry)
			}
		}
	}
	return result
}
func (s *Service) linuxIcon(name string) string {
	name = trimJS(name)
	if name == "" {
		return ""
	}
	if strings.HasPrefix(name, "/") && s.primitives.PathExists(name) {
		return name
	}
	ext := hostExt(name)
	base := strings.TrimSuffix(name, ext)
	extensions := []string{ext}
	if ext == "" {
		extensions = []string{".png", ".svg", ".xpm"}
	}
	for _, root := range []string{"/usr/share/pixmaps", "/usr/local/share/pixmaps", path.Join(s.options.Home, ".local", "share", "pixmaps")} {
		for _, ext := range extensions {
			file := path.Join(root, base+ext)
			if s.primitives.PathExists(file) {
				return file
			}
		}
	}
	for _, root := range []string{path.Join(s.options.Home, ".local", "share", "icons"), "/usr/local/share/icons", "/usr/share/icons"} {
		for _, theme := range s.primitives.ReadDirNames(root) {
			for _, sub := range []string{"scalable/apps", "512x512/apps", "256x256/apps", "128x128/apps", "64x64/apps", "48x48/apps", "32x32/apps", "apps/scalable", "apps/64", "apps/48", "apps/32"} {
				for _, ext := range extensions {
					file := path.Join(root, theme, sub, base+ext)
					if s.primitives.PathExists(file) {
						return file
					}
				}
			}
		}
	}
	return ""
}
