package application

import (
	"testing"
)

func TestWorktreeCommandRetainsOriginalOptionPresenceAndName(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		enabled bool
		name    *string
		base    string
	}{
		{[]string{"--tools", "Read"}, false, nil, ""},
		{[]string{"--tools", "Read", "--worktree"}, true, nil, ""},
		{[]string{"--tools", "Read", "-w", "private"}, true, worktreeName("private"), ""},
		{[]string{"--tools", "Read", "--worktree="}, true, worktreeName(""), ""},
		{[]string{"--tools", "Read", "--worktree", "#12", "--worktree-base-ref", "feature"}, true, worktreeName("#12"), "feature"},
	} {
		command, parsed, e := ParseCommand(tc.args, "fixture", t.TempDir(), t.TempDir())
		if e != nil || parsed.Parsed == nil {
			t.Fatal(tc.args, e, parsed)
		}
		got := command.Arguments.Worktree
		if (got != nil) != tc.enabled {
			t.Fatal("worktree presence", tc.args, got)
		}
		if got != nil {
			if (got.Name != nil) != (tc.name != nil) || got.Name != nil && *got.Name != *tc.name || got.BaseRef != tc.base {
				t.Fatal("worktree name semantics", tc.args, got)
			}
		}
	}
}
func worktreeName(s string) *string { return &s }
