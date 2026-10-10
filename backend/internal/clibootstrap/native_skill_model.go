package clibootstrap

import (
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
)

func (m *MainPrint) nativeSkillModel(scope *cooperative.Scope, model, current string) (string, error) {
	if !scope.BelongsTo(m.Owner) || m.nativeModelSelector == nil || m.Startup == nil || m.options.Native == nil {
		return "", cooperative.ErrRestricted
	}
	disabled := skillTruthy(m.options.Native.Environment["CLAUDE_CODE_DISABLE_1M_CONTEXT"])
	has := func(value string) bool {
		lower := strings.ToLower(value)
		return !disabled && (strings.Contains(lower, "[1m]") || strings.HasSuffix(lower, ":1m"))
	}
	if has(model) || !has(current) {
		return model, nil
	}
	parsed, e := m.nativeModelSelector.Parse(quoteMain(model))
	if e != nil {
		return "", e
	}
	canonical, e := decodeNativeModel(parsed)
	if e != nil {
		return "", e
	}
	supports, e := (modelaccounting.RestoredModelResolver{Environment: m.options.Native.Environment, Settings: m.Startup.GetInitialSettings}).Supports1M(canonical)
	if e != nil {
		return "", e
	}
	if supports {
		return model + "[1m]", nil
	}
	return model, nil
}

func skillTruthy(value string) bool {
	switch strings.ToLower(strings.TrimFunc(value, skillWhitespace)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
func skillWhitespace(r rune) bool {
	return r >= 9 && r <= 13 || r == ' ' || r == '\u00a0' || r == '\u1680' || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000 || r == 0xfeff
}
