package clibootstrap

import (
	"context"
	"os"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/heapdump"
)

func (m *MainPrint) nativeHeapDump(ctx context.Context, scope *cooperative.Scope) (string, error) {
	if !scope.BelongsTo(m.Owner) || m.options.Native == nil {
		return "", cooperative.ErrRestricted
	}
	n := m.options.Native
	sid := m.Controller.SessionID()
	// Native IO borrows the actual activated session's identity once, so resume
	// and fork cannot write an earlier startup ID into either artifact name.
	future, err := nativeSkillWork(m, scope, func() (string, error) {
		desktop := heapdump.Desktop(m.options.Platform, n.Home, n.Environment["USERPROFILE"], func(path string) bool { _, err := n.stat(path); return err == nil }, func(path string) ([]string, error) {
			entries, err := n.readDir(path)
			names := []string{}
			for _, entry := range entries {
				names = append(names, entry.Name())
			}
			return names, err
		})
		result := heapdump.Dump(heapdump.Options{DesktopPath: desktop, SessionID: sid, Version: m.options.Version, Now: n.Now, Capture: func() heapdump.Diagnostics {
			return heapdump.Capture(time.UnixMilli(int64(m.processStartMillis)), n.Now())
		}, Mkdir: func(path string) error {
			if err := n.check("heap-directory", path); err != nil {
				return err
			}
			return os.MkdirAll(path, 0777)
		}, WriteDiagnostics: func(path string, data []byte) error {
			if err := n.check("heap-diagnostics", path); err != nil {
				return err
			}
			return os.WriteFile(path, data, 0600)
		}, WriteHeap: func(path string) error {
			if err := n.check("heap-profile", path); err != nil {
				return err
			}
			return heapdump.WriteProfile(path)
		}}, "manual", 0)
		return result.Text(), nil
	})
	if err != nil {
		return "", err
	}
	return future.Await(scope)
}
