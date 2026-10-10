package voice

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"unsafe"
)

func TestNativeCABIActualPinnedCCompilerLayout(t *testing.T) {
	compiler, e := exec.LookPath("clang")
	if e != nil {
		t.Skip("native C compiler unavailable; platform ABI must be separately verified")
	}
	home := t.TempDir()
	header, e := filepath.Abs("testdata/native_abi.h")
	if e != nil {
		t.Fatal(e)
	}
	source := fmt.Sprintf(`#include <stdio.h>
#include <stddef.h>
#include %q
int main(){printf("{\"recognizer\":%%zu,\"model\":%%zu,\"sense\":%%zu,\"vad\":%%zu,\"segment\":%%zu,\"senseOffset\":%%zu,\"tokensOffset\":%%zu,\"vadTenOffset\":%%zu}",sizeof(SherpaOnnxOfflineRecognizerConfig),sizeof(SherpaOnnxOfflineModelConfig),sizeof(SherpaOnnxOfflineSenseVoiceModelConfig),sizeof(SherpaOnnxVadModelConfig),sizeof(SherpaOnnxSpeechSegment),offsetof(SherpaOnnxOfflineModelConfig,sense_voice),offsetof(SherpaOnnxOfflineModelConfig,tokens),offsetof(SherpaOnnxVadModelConfig,ten_vad));}
`, header)
	path := filepath.Join(home, "abi.c")
	_ = os.WriteFile(path, []byte(source), 0600)
	binary := filepath.Join(home, "abi")
	cmd := exec.CommandContext(context.Background(), compiler, path, "-o", binary)
	cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin", "TMPDIR=" + home}
	if data, e := cmd.CombinedOutput(); e != nil {
		t.Fatal(e, string(data))
	}
	data, e := exec.CommandContext(context.Background(), binary).Output()
	if e != nil {
		t.Fatal(e)
	}
	var actual map[string]uintptr
	if e = json.Unmarshal(data, &actual); e != nil {
		t.Fatal(e)
	}
	expected := map[string]uintptr{"recognizer": unsafe.Sizeof(SherpaOnnxOfflineRecognizerConfig{}), "model": unsafe.Sizeof(SherpaOnnxOfflineModelConfig{}), "sense": unsafe.Sizeof(SherpaOnnxOfflineSenseVoiceModelConfig{}), "vad": unsafe.Sizeof(SherpaOnnxVadModelConfig{}), "segment": unsafe.Sizeof(SherpaOnnxSpeechSegment{}), "senseOffset": unsafe.Offsetof(SherpaOnnxOfflineModelConfig{}.Sense_voice), "tokensOffset": unsafe.Offsetof(SherpaOnnxOfflineModelConfig{}.Tokens), "vadTenOffset": unsafe.Offsetof(SherpaOnnxVadModelConfig{}.Ten_vad)}
	for key, value := range expected {
		if actual[key] != value {
			t.Errorf("%s pinned C=%d Go=%d", key, actual[key], value)
		}
	}
}
func TestActualPinnedNativeLibraryLoadsWithoutNodeOrCgo(t *testing.T) {
	root := os.Getenv("CC_HAHA_VOICE_NATIVE_FIXTURE_ROOT")
	if root == "" {
		t.Skip("separate pinned native package evidence")
	}
	api, e := openNativeAPI(root)
	if e != nil {
		t.Fatal(e)
	}
	defer api.close()
	if api.version() != SherpaVersion {
		t.Fatal(api.version())
	}
}
