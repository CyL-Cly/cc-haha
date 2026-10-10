package whatsappcandidate

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestActualOriginalAddressTransactionOrderingMatchesNativeGate(t *testing.T) {
	home := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", nodeSourceFixturePath("node_transaction.fixture.ts"))
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_WHATSAPP_LEAF=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var expected struct {
		Events []string
		Writes []nodeObject
	}
	if e = json.Unmarshal(data, &expected); e != nil {
		t.Fatal(e)
	}
	s := NodeSignalStore{}
	var mu sync.Mutex
	events := []string{}
	writes := []nodeObject{}
	note := func(v string) { mu.Lock(); events = append(events, v); mu.Unlock() }
	write := func(id string, value int) {
		mu.Lock()
		writes = append(writes, nodeObject{"session": nodeJSON(map[string]any{id: map[string]any{"value": value}})})
		mu.Unlock()
	}
	entered := make(chan struct{})
	held := make(chan struct{})
	firstDone := make(chan struct{})
	secondDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		release, e := s.acquireAddress(ctx, "A")
		if e != nil {
			return
		}
		defer release()
		note("A1 begin")
		close(entered)
		<-held
		write("A1", 1)
		note("A1 end")
	}()
	<-entered
	go func() {
		defer close(secondDone)
		release, e := s.acquireAddress(ctx, "A")
		if e != nil {
			return
		}
		defer release()
		note("A2 begin")
		write("A2", 2)
		note("A2 end")
	}()
	other, e := s.acquireAddress(ctx, "B")
	if e != nil {
		close(held)
		t.Fatal(e)
	}
	note("B begin")
	write("B", 3)
	note("B end")
	other()
	close(held)
	<-firstDone
	<-secondDone
	if !bytes.Equal(nodeJSON(events), nodeJSON(expected.Events)) || !bytes.Equal(nodeJSON(writes), nodeJSON(expected.Writes)) {
		t.Fatalf("native events%s/writes%s Source events%s/writes%s", nodeJSON(events), nodeJSON(writes), nodeJSON(expected.Events), nodeJSON(expected.Writes))
	}
}
