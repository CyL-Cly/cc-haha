package clibootstrap

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolsearch"
)

func (m *MainPrint) nativeToolSearchBindings() *builtintools.ToolSearchBindings {
	if m.toolSearch == nil {
		m.toolSearch = toolsearch.New()
	}
	return &builtintools.ToolSearchBindings{Service: m.toolSearch, Enabled: func(scope *cooperative.Scope) (bool, error) {
		if !scope.BelongsTo(m.Owner) {
			return false, cooperative.ErrRestricted
		}
		return toolsearch.Optimistic(m.options.Native.Environment), nil
	}}
}
