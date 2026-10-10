package opentargets

import (
	"context"
	"path"
	"strings"
)

type definition struct {
	id, kind, label, icon string
	platforms             []string
	commands, apps, icons map[string][]string
	windowsExecutables    []string
	fallback              bool
}

func definitions(home string) []definition {
	common := []string{"darwin", "win32", "linux"}
	ide := func(id, label string, platforms []string, darwin, windows, linux, exe []string, app string) definition {
		d := definition{id: id, kind: "ide", label: label, icon: id, platforms: platforms, commands: map[string][]string{"darwin": darwin, "win32": windows, "linux": linux}, windowsExecutables: exe, apps: map[string][]string{}}
		if app != "" {
			d.apps["darwin"] = []string{"/Applications/" + app, path.Join(home, "Applications", app)}
		}
		return d
	}
	return []definition{
		ide("vscode", "VS Code", common, []string{"code"}, []string{"code.cmd", "code.exe"}, []string{"code"}, []string{"Code.exe"}, "Visual Studio Code.app"),
		ide("cursor", "Cursor", common, []string{"cursor"}, []string{"cursor.cmd", "cursor.exe"}, []string{"cursor"}, []string{"Cursor.exe"}, "Cursor.app"),
		ide("zed", "Zed", common, []string{"zed"}, []string{"zed.exe"}, []string{"zed", "zeditor"}, []string{"zed.exe"}, "Zed.app"),
		ide("sublime", "Sublime Text", common, []string{"subl"}, []string{"subl.exe", "subl"}, []string{"subl"}, []string{"sublime_text.exe", "subl.exe"}, "Sublime Text.app"),
		ide("antigravity", "Antigravity", []string{"darwin"}, []string{"antigravity"}, nil, nil, nil, "Antigravity.app"),
		ide("goland", "GoLand", common, []string{"goland"}, []string{"goland64.exe", "goland.cmd"}, []string{"goland"}, []string{"goland64.exe", "goland.exe"}, "GoLand.app"),
		ide("pycharm", "PyCharm", common, []string{"pycharm"}, []string{"pycharm64.exe", "pycharm.cmd"}, []string{"pycharm"}, []string{"pycharm64.exe", "pycharm.exe"}, "PyCharm.app"),
		{id: "finder", kind: "file_manager", label: "Finder", icon: "finder", platforms: []string{"darwin"}, icons: map[string][]string{"darwin": {"/System/Library/CoreServices/Finder.app/Contents/Resources/Finder.icns"}}, fallback: true},
		{id: "explorer", kind: "file_manager", label: "Explorer", icon: "folder", platforms: []string{"win32"}, fallback: true},
		{id: "file-manager", kind: "file_manager", label: "File Manager", icon: "folder", platforms: []string{"linux"}, fallback: true},
	}
}
func (d definition) supported(platform string) bool {
	for _, p := range d.platforms {
		if p == platform {
			return true
		}
	}
	return false
}
func (s *Service) definition(id string) (definition, bool) {
	for _, d := range s.definitions {
		if d.id == id {
			return d, true
		}
	}
	return definition{}, false
}
func (s *Service) detected(ctx context.Context, d definition) bool {
	p := s.options.Platform
	if !d.supported(p) {
		return false
	}
	if d.fallback {
		if p == "linux" {
			return s.primitives.CommandExists(ctx, "xdg-open")
		}
		return true
	}
	if p == "darwin" && len(d.apps[p]) > 0 {
		for _, a := range d.apps[p] {
			if s.primitives.PathExists(a) {
				return true
			}
		}
		return false
	}
	if p == "win32" {
		return s.windowsApplication(ctx, d) != ""
	}
	for _, a := range d.apps[p] {
		if s.primitives.PathExists(a) {
			return true
		}
	}
	for _, c := range d.commands[p] {
		if s.primitives.CommandExists(ctx, c) {
			return true
		}
	}
	return false
}
func (s *Service) windowsApplication(ctx context.Context, d definition) string {
	for _, a := range d.apps["win32"] {
		if s.primitives.PathExists(a) {
			return a
		}
	}
	for _, c := range d.commands["win32"] {
		command, err := s.primitives.ResolveCommand(ctx, c)
		if err != nil || command == "" {
			continue
		}
		if a := s.windowsExecutable(command, d); a != "" {
			return a
		}
	}
	return ""
}
func (s *Service) windowsExecutable(command string, d definition) string {
	ext := strings.ToLower(winExt(command))
	if ext == ".exe" && s.primitives.PathExists(command) {
		return command
	}
	if ext != ".cmd" && ext != ".bat" {
		return ""
	}
	names := d.windowsExecutables
	if names == nil {
		for _, c := range d.commands["win32"] {
			if strings.EqualFold(winExt(c), ".exe") {
				names = append(names, c)
			}
		}
	}
	current := winDir(command)
	for depth := 0; depth < 5; depth++ {
		for _, name := range names {
			candidate := winJoin(current, name)
			if s.primitives.PathExists(candidate) {
				return candidate
			}
		}
		next := winDir(current)
		if next == "" || next == current {
			break
		}
		current = next
	}
	return ""
}
func (s *Service) launchPlan(ctx context.Context, d definition, target resolvedPath) *launchPlan {
	p := s.options.Platform
	if !d.supported(p) {
		return nil
	}
	if d.fallback {
		switch p {
		case "darwin":
			if d.kind == "file_manager" && !target.directory {
				return &launchPlan{"open", []string{"-R", target.path}}
			}
			return &launchPlan{"open", []string{target.path}}
		case "win32":
			if d.kind == "file_manager" && !target.directory {
				return &launchPlan{"explorer.exe", []string{"/select," + target.path}}
			}
			return &launchPlan{"cmd.exe", []string{"/d", "/c", "start", "", target.path}}
		case "linux":
			targetPath := target.path
			if !target.directory {
				targetPath = hostDir(target.path)
			}
			return &launchPlan{"xdg-open", []string{targetPath}}
		}
		return nil
	}
	if p == "darwin" {
		for _, a := range d.apps[p] {
			if s.primitives.PathExists(a) {
				return &launchPlan{"open", []string{"-a", a, target.path}}
			}
		}
	}
	if p == "win32" {
		if a := s.windowsApplication(ctx, d); a != "" {
			return &launchPlan{a, []string{target.path}}
		}
		return nil
	}
	for _, c := range d.commands[p] {
		if s.primitives.CommandExists(ctx, c) {
			return &launchPlan{c, []string{target.path}}
		}
	}
	return nil
}
