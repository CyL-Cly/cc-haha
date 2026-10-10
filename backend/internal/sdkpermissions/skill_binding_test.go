package sdkpermissions

import (
	"reflect"
	"testing"
)

func TestExplicitSkillToolRequiresActualResolverAndExecutorBindings(t *testing.T) {
	if _, err := nativeToolList([]string{"Skill,Read"}); err == nil {
		t.Fatal("unbound Skill tool admitted")
	}
	if _, err := nativeToolList([]string{"Skill(example:*)"}, false, false, true, true, false); err == nil {
		t.Fatal("Web bindings admitted unbound Skill")
	}
	got, err := nativeToolList([]string{"Skill(example:*),Skill(*),Read"}, false, false, false, false, true)
	if err != nil || !reflect.DeepEqual(got, []string{"Skill(example:*)", "Skill", "Read"}) {
		t.Fatal("actual Skill rule parsing changed", got, err)
	}
	if _, err := nativeToolList([]string{"EnterWorktree"}, false, false, false, false, true); err == nil {
		t.Fatal("Skill binding broadened unrelated tool")
	}
}
