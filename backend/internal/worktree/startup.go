package worktree

import (
	"errors"
	"github.com/dop251/goja"
	"math"
	"regexp"
	"strconv"
)

// Startup preserves CLI option absence separately from an explicitly provided
// empty name; Source validates that empty slug rather than replacing it.
type Startup struct {
	Name    *string
	BaseRef string
	Tmux    bool
}

var pullReference = regexp.MustCompile(`(?i)^https?://[^/]+/[^/]+/[^/]+/pull/([0-9]+)/?(?:[?#][^\r\n\x{2028}\x{2029}]*)?$`)
var hashReference = regexp.MustCompile(`^#([0-9]+)$`)

func ParsePRReference(value string) (*float64, error) {
	match := pullReference.FindStringSubmatch(value)
	if match == nil {
		match = hashReference.FindStringSubmatch(value)
	}
	if len(match) != 2 {
		return nil, nil
	}
	parsed, e := strconv.ParseFloat(match[1], 64)
	if e != nil && !(errors.Is(e, strconv.ErrRange) && math.IsInf(parsed, 1)) {
		return nil, e
	}
	return &parsed, nil
}

// PRNumberString uses ECMAScript Number display, including its rounded decimal
// integer and exponential thresholds. Source interpolates this into both the
// worktree slug and the git pull ref, before its ordinary slug validation.
func PRNumberString(number float64) string { return goja.New().ToValue(number).String() }
