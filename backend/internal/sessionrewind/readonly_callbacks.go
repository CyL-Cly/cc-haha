package sessionrewind

import "strings"

func member(value string, values ...string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func (c *RecordedCommandClassifier) policyDangerous(name, command string, args []string) (bool, error) {
	switch name {
	case "git reflog":
		for _, arg := range args {
			if arg != "" && !strings.HasPrefix(arg, "-") {
				return member(arg, "expire", "delete", "exists"), nil
			}
		}
		return false, nil
	case "git remote show":
		positional := []string{}
		for _, arg := range args {
			if arg != "-n" {
				positional = append(positional, arg)
			}
		}
		return len(positional) != 1 || !jsMatch(`^[a-zA-Z0-9_-]+$`, positional[0]), nil
	case "git remote":
		for _, arg := range args {
			if arg != "-v" && arg != "--verbose" {
				return true, nil
			}
		}
		return false, nil
	case "git tag", "git branch":
		seenList, seenDash, lastFlag := false, false, ""
		for i := 0; i < len(args); {
			token := args[i]
			if token == "" {
				i++
				continue
			}
			if token == "--" && !seenDash {
				seenDash = true
				lastFlag = ""
				i++
				continue
			}
			if !seenDash && strings.HasPrefix(token, "-") {
				if token == "--list" || token == "-l" || (len(token) > 2 && !strings.HasPrefix(token, "--") && !strings.Contains(token, "=") && strings.Contains(token[1:], "l")) {
					seenList = true
				}
				if flag, _, ok := strings.Cut(token, "="); ok {
					lastFlag = flag
					i++
				} else {
					lastFlag = token
					takesArg := member(token, "--contains", "--no-contains", "--points-at", "--sort")
					if name == "git tag" {
						takesArg = takesArg || member(token, "--merged", "--no-merged", "--format", "-n")
					}
					if takesArg {
						i += 2
					} else {
						i++
					}
				}
			} else {
				optional := name == "git branch" && member(lastFlag, "--merged", "--no-merged")
				if !seenList && !optional {
					return true, nil
				}
				i++
			}
		}
		return false, nil
	case "ps":
		for _, arg := range args {
			if !strings.HasPrefix(arg, "-") && jsMatch(`^[a-zA-Z]*e[a-zA-Z]*$`, arg) {
				return true, nil
			}
		}
		return false, nil
	case "date":
		for i := 0; i < len(args); {
			token := args[i]
			if strings.HasPrefix(token, "--") && strings.Contains(token, "=") {
				i++
			} else if strings.HasPrefix(token, "-") {
				if member(token, "-d", "--date", "-r", "--reference", "--iso-8601", "--rfc-3339") {
					i += 2
				} else {
					i++
				}
			} else {
				if !strings.HasPrefix(token, "+") {
					return true, nil
				}
				i++
			}
		}
		return false, nil
	case "lsof":
		for _, arg := range args {
			if strings.HasPrefix(arg, "+m") {
				return true, nil
			}
		}
		return false, nil
	case "tput":
		afterDash := false
		for i := 0; i < len(args); {
			token := args[i]
			if token == "--" {
				afterDash = true
				i++
			} else if !afterDash && strings.HasPrefix(token, "-") {
				if token == "-S" || (!strings.HasPrefix(token, "--") && len(token) > 2 && strings.Contains(token, "S")) {
					return true, nil
				}
				if token == "-T" {
					i += 2
				} else {
					i++
				}
			} else {
				if member(token, "init", "reset", "rs1", "rs2", "rs3", "is1", "is2", "is3", "iprog", "if", "rf", "clear", "flash", "mc0", "mc4", "mc5", "mc5i", "mc5p", "pfkey", "pfloc", "pfx", "pfxl", "smcup", "rmcup") {
					return true, nil
				}
				i++
			}
		}
		return false, nil
	case "pyright":
		for _, arg := range args {
			if arg == "--watch" || arg == "-w" {
				return true, nil
			}
		}
		return false, nil
	case "sed":
		safe, err := c.sedAllowed(command)
		return !safe, err
	}
	if strings.HasPrefix(name, "gh ") {
		for _, token := range args {
			if token == "" {
				continue
			}
			value := token
			if strings.HasPrefix(token, "-") {
				_, inline, ok := strings.Cut(token, "=")
				if !ok || inline == "" {
					continue
				}
				value = inline
			}
			if strings.Contains(value, "://") || strings.Contains(value, "@") || strings.Count(value, "/") >= 2 {
				return true, nil
			}
		}
		return false, nil
	}
	return false, &UnsupportedRecordedCommand{"command-policy callback " + name}
}
