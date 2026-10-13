// Package windowspaths contains the Source's lexical Windows path boundary.
// None of these helpers accesses the filesystem, including UNC paths.
package windowspaths

import (
	"errors"
	"path"
	"strings"

	"golang.org/x/text/unicode/norm"
)

func IsUNC(value string) bool {
	return strings.HasPrefix(value, `\\`) || strings.HasPrefix(value, "//")
}

func driveLetter(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

func WindowsToPOSIX(value string) string {
	if strings.HasPrefix(value, `\\`) {
		return strings.ReplaceAll(value, `\`, "/")
	}
	if len(value) >= 3 && driveLetter(value[0]) && value[1] == ':' && (value[2] == '\\' || value[2] == '/') {
		return "/" + strings.ToLower(value[:1]) + strings.ReplaceAll(value[2:], `\`, "/")
	}
	return strings.ReplaceAll(value, `\`, "/")
}

func POSIXToWindows(value string) string {
	if strings.HasPrefix(value, "//") {
		return strings.ReplaceAll(value, "/", `\`)
	}
	start := 0
	if strings.HasPrefix(value, "/cygdrive/") {
		start = len("/cygdrive/")
	} else if strings.HasPrefix(value, "/") {
		start = 1
	}
	if start > 0 && len(value) > start && driveLetter(value[start]) && (len(value) == start+1 || value[start+1] == '/') {
		rest := value[start+1:]
		if rest == "" {
			rest = "/"
		}
		return strings.ToUpper(value[start:start+1]) + ":" + strings.ReplaceAll(rest, "/", `\`)
	}
	return strings.ReplaceAll(value, "/", `\`)
}

// windowsRoot separates Node win32's drive/UNC device from its remaining path.
func windowsRoot(value string) (device, rest string, absolute bool) {
	value = strings.ReplaceAll(value, "/", `\`)
	if len(value) >= 2 && driveLetter(value[0]) && value[1] == ':' {
		return value[:2], strings.TrimLeft(value[2:], `\`), len(value) >= 3 && value[2] == '\\'
	}
	if strings.HasPrefix(value, `\\`) && len(value) > 2 && value[2] != '\\' {
		parts := strings.FieldsFunc(value[2:], func(r rune) bool { return r == '\\' })
		if len(parts) >= 2 {
			device = `\\` + parts[0] + `\` + parts[1]
			return device, strings.Join(parts[2:], `\`), true
		}
	}
	return "", strings.TrimLeft(value, `\`), strings.HasPrefix(value, `\`)
}

func IsWindowsAbsolute(value string) bool { _, _, absolute := windowsRoot(value); return absolute }

func JoinWindows(values ...string) string {
	parts := []string{}
	for _, value := range values {
		if value != "" {
			parts = append(parts, value)
		}
	}
	joined := strings.Join(parts, `\`)
	if len(parts) > 0 {
		first := strings.ReplaceAll(parts[0], "/", `\`)
		unc := len(first) > 2 && first[0] == '\\' && first[1] == '\\' && first[2] != '\\'
		if !unc {
			count := 0
			for count < len(joined) && (joined[count] == '\\' || joined[count] == '/') {
				count++
			}
			if count > 1 {
				joined = `\` + joined[count:]
			}
		}
	}
	return CleanWindows(joined)
}

func DirWindows(value string) string {
	if value == "" {
		return "."
	}
	sep := func(index int) bool { return value[index] == '\\' || value[index] == '/' }
	rootEnd := 0
	if len(value) >= 2 && driveLetter(value[0]) && value[1] == ':' {
		rootEnd = 2
		if len(value) > 2 && sep(2) {
			rootEnd = 3
		}
	} else if sep(0) {
		rootEnd = 1
		if len(value) > 1 && sep(1) {
			index := 2
			for index < len(value) && !sep(index) {
				index++
			}
			if index > 2 && index < len(value) {
				for index < len(value) && sep(index) {
					index++
				}
				share := index
				for index < len(value) && !sep(index) {
					index++
				}
				if index > share {
					rootEnd = index
					if index < len(value) {
						rootEnd++
					}
				}
			}
		}
	}
	end := len(value)
	for end > rootEnd && sep(end-1) {
		end--
	}
	for index := end - 1; index >= rootEnd; index-- {
		if sep(index) {
			return value[:index]
		}
	}
	if rootEnd > 0 {
		return value[:rootEnd]
	}
	return "."
}

func CleanWindows(value string) string {
	if value == "" {
		return "."
	}
	device, rest, absolute := windowsRoot(value)
	trailing := strings.HasSuffix(value, `\`) || strings.HasSuffix(value, "/")
	parts := []string{}
	for _, part := range strings.FieldsFunc(rest, func(r rune) bool { return r == '\\' }) {
		switch part {
		case ".":
		case "..":
			if len(parts) > 0 && parts[len(parts)-1] != ".." {
				parts = parts[:len(parts)-1]
			} else if !absolute {
				parts = append(parts, part)
			}
		default:
			parts = append(parts, part)
		}
	}
	output := device
	if absolute {
		output += `\`
	}
	output += strings.Join(parts, `\`)
	if output == "" || !absolute && len(parts) == 0 {
		output += "."
	}
	if trailing && !strings.HasSuffix(output, `\`) {
		output += `\`
	}
	return output
}

// ResolveWindows is node:path.win32.resolve(cwd, value), without expandPath's
// home, whitespace or NFC processing. Executable discovery uses raw paths.
func ResolveWindows(value, cwd string) string {
	device, rest, absolute := windowsRoot(value)
	baseDevice, _, _ := windowsRoot(cwd)
	var output string
	switch {
	case absolute && device != "":
		output = CleanWindows(value)
	case absolute:
		output = CleanWindows(baseDevice + `\` + rest)
	case device != "" && strings.EqualFold(device, baseDevice):
		output = CleanWindows(device + cwd[len(baseDevice):] + `\` + rest)
	case device != "":
		output = CleanWindows(windowsDriveDirectory(device) + `\` + rest)
	default:
		output = CleanWindows(cwd + `\` + value)
	}
	resolvedDevice, _, _ := windowsRoot(output)
	if len(output) > len(resolvedDevice)+1 {
		output = strings.TrimRight(output, `\`)
	}
	return output
}

// ExpandWindows mirrors utils/path.expandPath; /cygdrive is converted only by
// the explicit POSIXToWindows helper, not silently added to Source expandPath.
func ExpandWindows(value, cwd, home string) (string, error) {
	if strings.ContainsRune(value, 0) || strings.ContainsRune(cwd, 0) {
		return "", errors.New("Path contains null bytes")
	}
	value = strings.TrimFunc(value, jsSpace)
	switch {
	case value == "":
		value = CleanWindows(cwd)
	case value == "~":
		value = home
	case strings.HasPrefix(value, "~/"):
		value = CleanWindows(home + `\` + value[2:])
	default:
		if len(value) >= 3 && value[0] == '/' && driveLetter(value[1]) && value[2] == '/' {
			value = POSIXToWindows(value)
		}
		if IsWindowsAbsolute(value) {
			value = CleanWindows(value)
		} else {
			device, rest, _ := windowsRoot(value)
			if device != "" {
				baseDevice, _, _ := windowsRoot(cwd)
				if strings.EqualFold(device, baseDevice) {
					value = CleanWindows(device + cwd[len(baseDevice):] + `\` + rest)
				} else {
					value = CleanWindows(windowsDriveDirectory(device) + `\` + rest)
				}
			} else {
				value = CleanWindows(cwd + `\` + value)
			}
			// node:path.resolve removes a relative input's trailing separator.
			resolvedDevice, _, _ := windowsRoot(value)
			if len(value) > len(resolvedDevice)+1 {
				value = strings.TrimRight(value, `\`)
			}
		}
	}
	return norm.NFC.String(value), nil
}

func jsSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0xfeff, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// RelativeWindows follows filesystem.relativePath: conversion followed by
// POSIX relative, including different drive letters and UNC volumes.
func RelativeWindows(from, to string) string {
	from = path.Clean(WindowsToPOSIX(from))
	to = path.Clean(WindowsToPOSIX(to))
	left, right := strings.Split(strings.Trim(from, "/"), "/"), strings.Split(strings.Trim(to, "/"), "/")
	if from == "/" || from == "." {
		left = nil
	}
	if to == "/" || to == "." {
		right = nil
	}
	common := 0
	for common < len(left) && common < len(right) && left[common] == right[common] {
		common++
	}
	parts := []string{}
	for i := common; i < len(left); i++ {
		parts = append(parts, "..")
	}
	parts = append(parts, right[common:]...)
	return strings.Join(parts, "/")
}
