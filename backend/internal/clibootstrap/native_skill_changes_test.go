package clibootstrap

import (
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestManagedSkillSignalIsConfigScopedEnqueuesBeforeReturnAndUnsubscribes(t *testing.T) {
	root := t.TempDir()
	old, target := filepath.Join(root, "old"), filepath.Join(root, "migration-target")
	var oldCalls, targetCalls atomic.Int32
	failed := errors.New("actual refresh admission failed")
	unsubOld := subscribeNativeSkillChanges(old, func() error { oldCalls.Add(1); return nil })
	defer unsubOld()
	unsubTarget := subscribeNativeSkillChanges(target, func() error { targetCalls.Add(1); return failed })
	defer unsubTarget()
	if err := NotifyNativeSkillCommandsChanged(old); err != nil || oldCalls.Load() != 1 || targetCalls.Load() != 0 {
		t.Fatal("publication crossed migration directories or returned before admission", err)
	}
	if err := NotifyNativeSkillCommandsChanged(target); !errors.Is(err, failed) || targetCalls.Load() != 1 || oldCalls.Load() != 1 {
		t.Fatal("target admission failure was hidden or reached old config", err)
	}
	unsubOld()
	unsubOld()
	if err := NotifyNativeSkillCommandsChanged(old); err != nil || oldCalls.Load() != 1 {
		t.Fatal("closed subscription received another publication", err)
	}
	unsubTarget()
	nativeSkillChanges.Lock()
	defer nativeSkillChanges.Unlock()
	if _, exists := nativeSkillChanges.byConfig[nativeSkillConfigKey(old)]; exists {
		t.Fatal("closed old-config signal retained its owner")
	}
	if _, exists := nativeSkillChanges.byConfig[nativeSkillConfigKey(target)]; exists {
		t.Fatal("closed migrated-config signal retained its owner")
	}
}
