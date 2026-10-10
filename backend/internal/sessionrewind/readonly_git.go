package sessionrewind

import "strings"

// Only the early Git validator may skip the remaining security validators.
// The final recorded-command allowlist still runs for every fragment.
func nativeGitCommit(command, base string) (handled, allowed bool) {
	if base != "git" || !jsMatch(`^git\s+commit\s+`, command) || strings.Contains(command, `\`) {
		return false, false
	}
	r, _ := regexpNative("^git[ \\t]+commit[ \\t]+[^;&|`$<>()\\n\\r]*?-m[ \\t]+([\"'])([\\s\\S]*?)\\1(.*)$")
	m, err := r.FindStringMatch(command)
	if err != nil || m == nil {
		return false, false
	}
	quote, message, remainder := m.GroupByNumber(1).String(), m.GroupByNumber(2).String(), m.GroupByNumber(3).String()
	if quote == `"` && jsMatch("\\$\\(|`|\\$\\{", message) {
		return true, false
	}
	if jsMatch("[;|&()`]|\\$\\(|\\$\\{", remainder) {
		return false, false
	}
	if strings.ContainsAny(extractNativeQuotes(remainder, false).fully, "<>") {
		return false, false
	}
	if strings.HasPrefix(message, "-") {
		return true, false
	}
	return true, true
}

func nativeZshDangerous(command string) bool {
	trimmed := jsTrimCommand(command)
	r, _ := regexpNative(`\s+`)
	tokens, _ := r.Split(trimmed, -1)
	base := ""
	for _, token := range tokens {
		if jsMatch(`^[A-Za-z_]\w*=`, token) || member(token, "command", "builtin", "noglob", "nocorrect") {
			continue
		}
		base = token
		break
	}
	if member(base, "zmodload", "emulate", "sysopen", "sysread", "syswrite", "sysseek", "zpty", "ztcp", "zsocket", "mapfile", "zf_rm", "zf_mv", "zf_ln", "zf_chmod", "zf_chown", "zf_mkdir", "zf_rmdir", "zf_chgrp") {
		return true
	}
	return base == "fc" && jsMatch(`\s-\S*e`, trimmed)
}

func stripNativeComments(command string) string {
	kept := []string{}
	for _, line := range strings.Split(command, "\n") {
		trimmed := jsTrimCommand(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			kept = append(kept, line)
		}
	}
	if len(kept) == 0 {
		return command
	}
	return strings.Join(kept, "\n")
}

func (c *RecordedCommandClassifier) stripSafeWrappers(command string) string {
	env, _ := regexpNative(`^([A-Za-z_][A-Za-z0-9_]*)=([A-Za-z0-9_./:-]+)[ \t]+`)
	stripped, previous := command, ""
	for stripped != previous {
		previous = stripped
		stripped = stripNativeComments(stripped)
		if m, err := env.FindStringMatch(stripped); err == nil && m != nil {
			name := m.GroupByNumber(1).String()
			if member(name, readonlyCompile.policy.SafeEnvironment...) || (c.UserType == "ant" && member(name, readonlyCompile.policy.AntEnvironment...)) {
				stripped, _ = env.Replace(stripped, "", 0, 1)
			}
		}
	}
	previous = ""
	for stripped != previous {
		previous = stripped
		stripped = stripNativeComments(stripped)
		for _, pattern := range []string{
			`^timeout[ \t]+(?:(?:--(?:foreground|preserve-status|verbose)|--(?:kill-after|signal)=[A-Za-z0-9_.+-]+|--(?:kill-after|signal)[ \t]+[A-Za-z0-9_.+-]+|-v|-[ks][ \t]+[A-Za-z0-9_.+-]+|-[ks][A-Za-z0-9_.+-]+)[ \t]+)*(?:--[ \t]+)?\d+(?:\.\d+)?[smhd]?[ \t]+`,
			`^time[ \t]+(?:--[ \t]+)?`,
			`^nice(?:[ \t]+-n[ \t]+-?\d+|[ \t]+-\d+)?[ \t]+(?:--[ \t]+)?`,
			`^stdbuf(?:[ \t]+-[ioe][LN0-9]+)+[ \t]+(?:--[ \t]+)?`,
			`^nohup[ \t]+(?:--[ \t]+)?`,
		} {
			r, _ := regexpNative(pattern)
			stripped, _ = r.Replace(stripped, "", 0, 1)
		}
	}
	return jsTrimCommand(stripped)
}

func (c *RecordedCommandClassifier) normalizedCommands(command string) (git, cd bool) {
	command = c.stripSafeWrappers(jsTrimCommand(command))
	tokens, err := c.parse(command, false)
	if err != nil || len(tokens) == 0 {
		return jsMatch(`^git(?:\s|$)`, command), jsMatch(`^(?:cd|pushd|popd)(?:\s|$)`, command)
	}
	if tokens[0].isString {
		git = tokens[0].text == "git"
		cd = member(tokens[0].text, "cd", "pushd", "popd")
		if tokens[0].text == "xargs" {
			for _, token := range tokens {
				git = git || (token.isString && token.text == "git")
			}
		}
	}
	return git, cd
}

func gitInternalPath(path string) bool {
	if strings.HasPrefix(path, "./") {
		path = path[2:]
	} else if strings.HasPrefix(path, "/") {
		path = path[1:]
	}
	return path == "HEAD" || path == "objects" || strings.HasPrefix(path, "objects/") || path == "refs" || strings.HasPrefix(path, "refs/") || path == "hooks" || strings.HasPrefix(path, "hooks/")
}

func (c *RecordedCommandClassifier) writesGitInternals(commands []string) (bool, error) {
	for _, command := range commands {
		tokens, err := c.parse(jsTrimCommand(command), true)
		if err == nil {
			words := []string{}
			for _, token := range tokens {
				if token.isString {
					words = append(words, token.text)
				}
			}
			if len(words) > 0 && member(words[0], "mkdir", "touch", "mv", "cp") {
				afterDash := false
				for _, word := range words[1:] {
					if !afterDash && word == "--" {
						afterDash = true
						continue
					}
					if (afterDash || !strings.HasPrefix(word, "-")) && gitInternalPath(word) {
						return true, nil
					}
				}
			}
		}
		paths, err := c.outputTargets(command)
		if err != nil {
			return false, err
		}
		for _, path := range paths {
			if gitInternalPath(path) {
				return true, nil
			}
		}
	}
	return false, nil
}

func simpleOutputTarget(token shellToken) bool {
	return token.isString && token.text != "" && !strings.HasPrefix(token.text, "!") && !strings.HasPrefix(token.text, "=") && !strings.HasPrefix(token.text, "~") && !strings.ContainsAny(token.text, "$`*?[{")
}

// This projects only the targets from Source extractOutputRedirections. It
// neither rebuilds commands nor executes them.
func (c *RecordedCommandClassifier) outputTargets(command string) ([]string, error) {
	heredocs, err := nativeHeredocs(command, false)
	if err != nil {
		return nil, err
	}
	tokens, err := c.parse(joinContinuations(heredocs.processed), true)
	if err != nil {
		return []string{}, nil
	}
	targets := []string{}
	get := func(index int) shellToken {
		if index < 0 || index >= len(tokens) {
			return shellToken{}
		}
		return tokens[index]
	}
	fd := func(token shellToken) bool { return token.isString && jsMatch(`^\d+$`, jsTrimCommand(token.text)) }
	depth := 0
	for i, token := range tokens {
		prev, next, after, third := get(i-1), get(i+1), get(i+2), get(i+3)
		if token.op == "(" && prev.isString && strings.HasSuffix(prev.text, "$") {
			depth++
		} else if token.op == ")" && depth > 0 {
			depth--
		}
		if depth != 0 || !member(token.op, ">", ">>", ">&") {
			continue
		}
		if token.op == ">&" && fd(prev) && fd(next) {
			continue
		}
		target := next
		if next.text == "!" || next.op == "|" {
			target = after
		} else if token.op != ">&" && next.isString && len(next.text) > 1 && next.text[0] == '!' && !strings.ContainsRune("!-?", rune(next.text[1])) && !jsMatch(`^!\d`, next.text) {
			target.text = next.text[1:]
		} else if token.op != ">&" && next.op == "&" {
			target = after
			if after.text == "!" || after.op == "|" {
				target = third
			}
		}
		if simpleOutputTarget(target) && !(fd(target) && (token.op == ">&" || fd(prev))) {
			targets = append(targets, target.text)
		}
	}
	return targets, nil
}
