package clibootstrap

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
)

func (m *MainPrint) nativeBundledSkillDefinitions() ([]skillcommands.Definition, error) {
	m.ensureNativeSkillState()
	return skillcommands.NativeExternalBundled(skillcommands.BundledOptions{Environment: m.options.Native.Environment, IsGit: func(ctx context.Context) (bool, error) {
		if e := ctx.Err(); e != nil {
			return false, e
		}
		if !m.skillListing.gitLoaded {
			m.skillListing.gitLoaded = true
			m.skillListing.gitExists = usercontext.GitRoot(usercontext.OSMemoryFiles{}, m.state.Snapshot().CWD) != ""
		}
		return m.skillListing.gitExists, nil
	}})
}
