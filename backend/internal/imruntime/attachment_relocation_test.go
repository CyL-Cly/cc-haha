package imruntime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestActualIMOutboundRelocationUsesActiveConfigWithoutBroadeningWorkBoundary(t *testing.T) {
	private := t.TempDir()
	oldRoot, active := filepath.Join(private, "old"), filepath.Join(private, "active")
	target := filepath.Join(active, "image-cache", "image.png")
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(active, "cc-haha"), 0700); err != nil {
		t.Fatal(err)
	}
	data := []byte("actual migrated image")
	if err := os.WriteFile(target, data, 0600); err != nil {
		t.Fatal(err)
	}
	metadata, _ := json.Marshal(map[string]any{"version": 1, "previousRoots": []string{oldRoot}, "unknown": true})
	metadataPath := filepath.Join(active, "cc-haha", "storage-relocations.json")
	if err := os.WriteFile(metadataPath, metadata, 0600); err != nil {
		t.Fatal(err)
	}
	source := UploadSource{Kind: "path", Path: filepath.Join(oldRoot, "image-cache", "image.png")}
	image := LoadSafeOutboundImage(PendingUpload{Source: source}, active, active)
	if !image.OK || string(image.Buffer) != string(data) {
		t.Fatal("relocated physical image lost", image)
	}
	outside := filepath.Join(private, "work")
	if err := os.Mkdir(outside, 0700); err != nil {
		t.Fatal(err)
	}
	if image := LoadSafeOutboundImage(PendingUpload{Source: source}, outside, active); image.OK {
		t.Fatal("relocation broadened work authorization")
	}
	r := &FeishuRuntime{ctx: context.Background(), o: FeishuRuntimeOptions{Config: Config{ConfigDir: active}}}
	if image, err := r.materializeOutbound(source); err != nil || string(image.Buffer) != string(data) {
		t.Fatal("Feishu physical read did not resolve relocation", image, err)
	}
	if err := os.WriteFile(metadataPath, []byte(`{"version":2,"previousRoots":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if image := LoadSafeOutboundImage(PendingUpload{Source: source}, active, active); image.OK {
		t.Fatal("invalid migration metadata silently fell back")
	}
	if _, err := r.materializeOutbound(source); err == nil {
		t.Fatal("Feishu bypassed invalid migration metadata")
	}
}
