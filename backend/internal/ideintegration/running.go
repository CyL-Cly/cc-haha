package ideintegration

import (
	"context"
	"strings"
)

type runningIDEConfig struct {
	kind                string
	mac, windows, linux []string
}

var runningConfigs = []runningIDEConfig{
	{"cursor", []string{"Cursor Helper", "Cursor.app"}, []string{"cursor.exe"}, []string{"cursor"}},
	{"windsurf", []string{"Windsurf Helper", "Windsurf.app"}, []string{"windsurf.exe"}, []string{"windsurf"}},
	{"vscode", []string{"Visual Studio Code", "Code Helper"}, []string{"code.exe"}, []string{"code"}},
	{"intellij", []string{"IntelliJ IDEA"}, []string{"idea64.exe"}, []string{"idea", "intellij"}},
	{"pycharm", []string{"PyCharm"}, []string{"pycharm64.exe"}, []string{"pycharm"}},
	{"webstorm", []string{"WebStorm"}, []string{"webstorm64.exe"}, []string{"webstorm"}},
	{"phpstorm", []string{"PhpStorm"}, []string{"phpstorm64.exe"}, []string{"phpstorm"}},
	{"rubymine", []string{"RubyMine"}, []string{"rubymine64.exe"}, []string{"rubymine"}},
	{"clion", []string{"CLion"}, []string{"clion64.exe"}, []string{"clion"}},
	{"goland", []string{"GoLand"}, []string{"goland64.exe"}, []string{"goland"}},
	{"rider", []string{"Rider"}, []string{"rider64.exe"}, []string{"rider"}},
	{"datagrip", []string{"DataGrip"}, []string{"datagrip64.exe"}, []string{"datagrip"}},
	{"appcode", []string{"AppCode"}, []string{"appcode.exe"}, []string{"appcode"}},
	{"dataspell", []string{"DataSpell"}, []string{"dataspell64.exe"}, []string{"dataspell"}},
	{"aqua", nil, []string{"aqua64.exe"}, nil}, {"gateway", nil, []string{"gateway64.exe"}, nil}, {"fleet", nil, []string{"fleet.exe"}, nil},
	{"androidstudio", []string{"Android Studio"}, []string{"studio64.exe"}, []string{"android-studio"}},
}

func (s *Extensions) Running(ctx context.Context) []string {
	result := []string{}
	if s.options.RunShell == nil {
		return result
	}
	command := ""
	switch s.options.Platform {
	case "macos", "darwin":
		command = `ps aux | grep -E "Visual Studio Code|Code Helper|Cursor Helper|Windsurf Helper|IntelliJ IDEA|PyCharm|WebStorm|PhpStorm|RubyMine|CLion|GoLand|Rider|DataGrip|AppCode|DataSpell|Aqua|Gateway|Fleet|Android Studio" | grep -v grep`
	case "windows":
		command = `tasklist | findstr /I "Code.exe Cursor.exe Windsurf.exe idea64.exe pycharm64.exe webstorm64.exe phpstorm64.exe rubymine64.exe clion64.exe goland64.exe rider64.exe datagrip64.exe appcode.exe dataspell64.exe aqua64.exe gateway64.exe fleet.exe studio64.exe"`
	case "linux":
		command = `ps aux | grep -E "code|cursor|windsurf|idea|pycharm|webstorm|phpstorm|rubymine|clion|goland|rider|datagrip|dataspell|aqua|gateway|fleet|android-studio" | grep -v grep`
	default:
		return result
	}
	out, e := s.options.RunShell(ctx, command)
	if e != nil {
		return result
	}
	text := out.Stdout
	if s.options.Platform == "windows" || s.options.Platform == "linux" {
		text = strings.ToLower(text)
	}
	for _, config := range runningConfigs {
		words := config.mac
		if s.options.Platform == "windows" {
			words = config.windows
		} else if s.options.Platform == "linux" {
			words = config.linux
		}
		for _, word := range words {
			if strings.Contains(text, word) {
				if s.options.Platform == "linux" && config.kind == "vscode" && (strings.Contains(text, "cursor") || strings.Contains(text, "appcode")) {
					continue
				}
				result = append(result, config.kind)
				break
			}
		}
	}
	return result
}
