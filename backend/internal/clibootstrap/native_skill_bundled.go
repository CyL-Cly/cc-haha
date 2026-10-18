package clibootstrap

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
)

func (m *MainPrint) nativeBundledSkillDefinitions() ([]skillcommands.Definition, error) {
	return m.loadNativeBundledSkillDefinitions(false)
}

func (m *MainPrint) nativeBundledSkillCatalog() ([]skillcommands.Definition, error) {
	return m.loadNativeBundledSkillDefinitions(true)
}

func (m *MainPrint) loadNativeBundledSkillDefinitions(unfiltered bool) ([]skillcommands.Definition, error) {
	m.ensureNativeSkillState()
	load := skillcommands.NativeExternalBundled
	if unfiltered {
		load = skillcommands.NativeExternalBundledCatalog
	}
	return load(skillcommands.BundledOptions{Environment: m.options.Native.nativeEnvironmentSnapshot(), EnvironmentSnapshot: m.options.Native.nativeEnvironmentSnapshot, IsGit: func(ctx context.Context) (bool, error) {
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
