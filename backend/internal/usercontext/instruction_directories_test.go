package usercontext

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestAdditionalInstructionDirectoriesPreserveBothSourceMemoBoundaries(t *testing.T) {
	options := nativeOptions(t)
	memory, home := nativeMemory(t)
	memory.Normal.AdditionalDirectoriesEnabled = true
	options.Memory = memory
	env, err := clistartupenv.NewEnvironment(protocol.NewJSONObject())
	if err != nil {
		t.Fatal(err)
	}
	options.Environment = env
	writeMemory(t, filepath.Join(memory.Normal.CWD, "CLAUDE.md"), "INITIAL_DIRECTORY_MEMORY")
	external := filepath.Join(home, "external")
	writeMemory(t, filepath.Join(external, "CLAUDE.md"), "ADDED_DIRECTORY_MEMORY")
	service, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	first := service.GetUserContext()
	value, err := first.Await(context.Background())
	if err != nil || !strings.Contains(string(value.Get("claudeMd")), "INITIAL_DIRECTORY_MEMORY") {
		t.Fatal(value, err)
	}
	input := []string{external}
	service.SetAdditionalInstructionDirectories(input)
	input[0] = "mutated"
	if actual := service.GetUserContext(); actual != first {
		t.Fatal("bootstrap assignment cleared Source user-context memo")
	}
	service.ClearUserContext()
	sameFiles, err := service.GetUserContext().Await(context.Background())
	if err != nil || strings.Contains(string(sameFiles.Get("claudeMd")), "ADDED_DIRECTORY_MEMORY") {
		t.Fatal("user-context clear incorrectly cleared independent files memo", sameFiles, err)
	}
	service.RefreshMemoryFiles()
	refreshed, err := service.GetUserContext().Await(context.Background())
	if err != nil || !strings.Contains(string(refreshed.Get("claudeMd")), "ADDED_DIRECTORY_MEMORY") {
		t.Fatal("normal explicit discovery clear did not consume updated detached input", refreshed, err)
	}
}
