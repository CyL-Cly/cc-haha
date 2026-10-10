// Package releasenotes ports the original changelog cache and local command.
package releasenotes

import (
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
)

const ChangelogURL = "https://github.com/anthropics/claude-code/blob/main/CHANGELOG.md"
const RawChangelogURL = "https://raw.githubusercontent.com/anthropics/claude-code/refs/heads/main/CHANGELOG.md"

type Entry struct {
	Version string
	Notes   []string
}

func jsSpace(r rune) bool {
	return r >= 9 && r <= 13 || r == 32 || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000 || r == 0xfeff
}
func trim(s string) string { return strings.TrimFunc(s, jsSpace) }

func Parse(content string) []Entry {
	result := []Entry{}
	// RegExp /^## /gm considers all ECMAScript line terminators, whereas the
	// subsequent split('\n') intentionally does not normalize CR/LF.
	starts := []int{}
	for i := 0; i < len(content); i++ {
		if strings.HasPrefix(content[i:], "## ") && (i == 0 || content[i-1] == '\n' || content[i-1] == '\r' || i >= 3 && (content[i-3:i] == "\u2028" || content[i-3:i] == "\u2029")) {
			starts = append(starts, i+3)
		}
	}
	for i, start := range starts {
		end := len(content)
		if i+1 < len(starts) {
			end = starts[i+1] - 3
		}
		lines := strings.Split(trim(content[start:end]), "\n")
		version := trim(strings.SplitN(lines[0], " - ", 2)[0])
		if version == "" || version == "__proto__" {
			continue
		}
		notes := []string{}
		for _, line := range lines[1:] {
			line = trim(line)
			if strings.HasPrefix(line, "- ") {
				if note := trim(line[2:]); note != "" {
					notes = append(notes, note)
				}
			}
		}
		if len(notes) == 0 {
			continue
		}
		found := false
		for j := range result {
			if result[j].Version == version {
				result[j].Notes = notes
				found = true
				break
			}
		}
		if !found {
			result = append(result, Entry{version, notes})
		}
	}
	// Object.keys emits canonical uint32 property names before other keys.
	index := func(s string) (uint64, bool) {
		n, e := strconv.ParseUint(s, 10, 32)
		return n, e == nil && n < math.MaxUint32 && strconv.FormatUint(n, 10) == s
	}
	sort.SliceStable(result, func(i, j int) bool {
		a, ai := index(result[i].Version)
		b, bi := index(result[j].Version)
		if ai && bi {
			return a < b
		}
		return ai && !bi
	})
	return result
}

type version struct {
	numbers [3]uint64
	pre     []string
}

// Bun's comparator accepts loose prefixes, missing components (wildcards),
// leading zeroes, and a prerelease without '-'. These are observable in the
// original getAllReleaseNotes utility; npm's strict parser is not equivalent.
func parseVersion(input string) (version, error) {
	v := version{numbers: [3]uint64{math.MaxUint64, math.MaxUint64, math.MaxUint64}}
	s := trim(input)
	for len(s) > 0 && (s[0] == 'v' || s[0] == '=') {
		s = s[1:]
	}
	s = trim(s)
	if s == "" {
		return v, errors.New("invalid semver")
	}
	for component := 0; component < 3; component++ {
		end := 0
		for end < len(s) && s[end] >= '0' && s[end] <= '9' {
			end++
		}
		if end == 0 {
			if len(s) > 0 && (s[0] == 'x' || s[0] == 'X' || s[0] == '*') {
				return v, nil
			}
			return v, errors.New("invalid semver")
		}
		n, err := strconv.ParseUint(s[:end], 10, 64)
		if err != nil {
			return v, err
		}
		v.numbers[component] = n
		s = s[end:]
		if len(s) > 0 && s[0] == '.' {
			if component == 2 {
				return v, errors.New("invalid semver")
			}
			s = s[1:]
			continue
		}
		break
	}
	if s == "" || s[0] == '+' || jsSpace(rune(s[0])) {
		return v, nil
	}
	s = strings.TrimPrefix(s, "-")
	if end := strings.IndexByte(s, '+'); end >= 0 {
		s = s[:end]
	}
	if s == "" {
		return v, nil
	}
	for _, part := range strings.Split(s, ".") {
		if part == "" {
			return v, errors.New("invalid semver")
		}
		for _, c := range part {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return v, errors.New("invalid semver")
			}
		}
		v.pre = append(v.pre, part)
	}
	return v, nil
}

func Compare(a, b string) (int, error) {
	x, e := parseVersion(a)
	if e != nil {
		return 0, e
	}
	y, e := parseVersion(b)
	if e != nil {
		return 0, e
	}
	for i := 0; i < 3; i++ {
		if x.numbers[i] < y.numbers[i] {
			return -1, nil
		}
		if x.numbers[i] > y.numbers[i] {
			return 1, nil
		}
	}
	if len(x.pre) == 0 && len(y.pre) > 0 {
		return 1, nil
	}
	if len(y.pre) == 0 && len(x.pre) > 0 {
		return -1, nil
	}
	for i := 0; i < len(x.pre) && i < len(y.pre); i++ {
		a, b := x.pre[i], y.pre[i]
		na, ea := strconv.ParseUint(a, 10, 64)
		nb, eb := strconv.ParseUint(b, 10, 64)
		if ea == nil && eb == nil {
			if na < nb {
				return -1, nil
			}
			if na > nb {
				return 1, nil
			}
		} else if ea == nil {
			return -1, nil
		} else if eb == nil {
			return 1, nil
		} else {
			if a < b {
				return -1, nil
			}
			if a > b {
				return 1, nil
			}
		}
	}
	if len(x.pre) < len(y.pre) {
		return -1, nil
	}
	if len(x.pre) > len(y.pre) {
		return 1, nil
	}
	return 0, nil
}

func All(content string) []Entry {
	entries := Parse(content)
	// Source's comparator returns -1 even for equivalent version spellings.
	// Insertion preserves that comparator's reversal of those spellings.
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0; j-- {
			order, e := Compare(entries[j-1].Version, entries[j].Version)
			if e != nil {
				return []Entry{}
			}
			if order < 0 {
				break
			}
			entries[j-1], entries[j] = entries[j], entries[j-1]
		}
	}
	return entries
}
func Format(entries []Entry) string {
	var out []string
	for _, entry := range entries {
		out = append(out, "Version "+entry.Version+":\n· "+strings.Join(entry.Notes, "\n· "))
	}
	return strings.Join(out, "\n\n")
}
