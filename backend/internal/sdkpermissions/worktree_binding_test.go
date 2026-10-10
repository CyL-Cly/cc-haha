package sdkpermissions

import (
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestWorktreeRequiresActualSessionGitCwdAndPersistenceBindings(t *testing.T) {
	for _, name := range []string{"EnterWorktree", "ExitWorktree"} {
		if _, e := nativeToolList([]string{name}, false, false, false, false, false, false); e == nil {
			t.Fatal("unbound Worktree admitted", name)
		}
		if got, e := nativeToolList([]string{name + "(*)"}, false, false, false, false, false, false, true); e != nil || len(got) != 1 || got[0] != name {
			t.Fatal("bound Worktree rule", got, e)
		}
	}
	var settings protocol.JSONObject
	_ = settings.UnmarshalJSON([]byte(`{"worktree":{"sparsePaths":["src"]}}`))
	if e := ValidateNativeWithBindings(&settings, false, false, false, false, false, false); e == nil {
		t.Fatal("settings accepted without consumer")
	}
	if e := ValidateNativeWithBindings(&settings, false, false, false, false, false, true); e != nil {
		t.Fatal("actual Worktree settings consumer rejected", e)
	}
}
