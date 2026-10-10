package imruntime

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func whatsAppOldStore(t *testing.T, warn func(error)) *WhatsAppAuthStore {
	t.Helper()
	dir := t.TempDir()
	if e := os.WriteFile(filepath.Join(dir, "creds.json"), []byte(`{"registered":true,"future":{"preserve":true},"counter":0}`), 0600); e != nil {
		t.Fatal(e)
	}
	s, e := OpenWhatsAppAuthStore(dir, WhatsAppAuthOptions{Warn: warn})
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func TestWhatsAppCredsQueueReadsLiveStateFIFOAndCloseJoinsActualWrites(t *testing.T) {
	s := whatsAppOldStore(t, nil)
	path := filepath.Join(s.Dir, "creds.json")
	lock := whatsAppAuthFileLock(path)
	lock.Lock()
	defer func() {
		if s != nil {
			s.Close()
		}
	}()
	first, e := s.QueueSaveCreds()
	if e != nil {
		lock.Unlock()
		t.Fatal(e)
	}
	if e = s.UpdateCredentials(object(raw(map[string]any{"counter": 2}))); e != nil {
		lock.Unlock()
		t.Fatal(e)
	}
	second, e := s.QueueSaveCreds()
	if e != nil {
		lock.Unlock()
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e = s.Wait(ctx); !errors.Is(e, context.Canceled) {
		lock.Unlock()
		t.Fatal("wait cancellation", e)
	}
	closed := make(chan struct{})
	go func() { s.Close(); close(closed) }()
	select {
	case <-closed:
		lock.Unlock()
		t.Fatal("close did not join queued physical save")
	default:
	}
	lock.Unlock()
	<-first
	<-second
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("close not joined")
	}
	data, e := os.ReadFile(path)
	if e != nil || !bytes.Contains(data, []byte(`"counter":2`)) || !bytes.Contains(data, []byte(`"future":{"preserve":true}`)) {
		t.Fatal("queued live state lost", e, string(data))
	}
	if _, e = s.QueueSaveCreds(); e == nil {
		t.Fatal("closed store accepted write")
	}
	s = nil
}
func TestWhatsAppCredsFailureReportsButWaitResolvesAndBackupRemains(t *testing.T) {
	var mu sync.Mutex
	warnings := []error{}
	s := whatsAppOldStore(t, func(e error) { mu.Lock(); warnings = append(warnings, e); mu.Unlock() })
	defer s.Close()
	path := filepath.Join(s.Dir, "creds.json")
	if e := os.Remove(path); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(path, 0700); e != nil {
		t.Fatal(e)
	}
	if _, e := s.QueueSaveCreds(); e != nil {
		t.Fatal(e)
	}
	if e := s.Wait(context.Background()); e != nil {
		t.Fatal("recoverable queue write rejected wait", e)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(warnings) != 1 {
		t.Fatal("physical write failure not reported", len(warnings))
	}
}
func TestWhatsAppKeyStoreSharedPathMutexAndPartialFailureKeepsSiblingWrites(t *testing.T) {
	s := whatsAppOldStore(t, nil)
	defer s.Close()
	second, e := OpenWhatsAppAuthStore(s.Dir, WhatsAppAuthOptions{})
	if e != nil {
		t.Fatal(e)
	}
	defer second.Close()
	path := filepath.Join(s.Dir, "session-id.json")
	lock := whatsAppAuthFileLock(path)
	lock.Lock()
	done := make(chan error, 1)
	go func() {
		done <- second.SetKeys(object(raw(map[string]any{"session": map[string]any{"id": map[string]any{"value": 1}}})))
	}()
	select {
	case <-done:
		lock.Unlock()
		t.Fatal("shared path write escaped file mutex")
	default:
	}
	lock.Unlock()
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	bad := filepath.Join(s.Dir, "session-bad.json")
	if e := os.Mkdir(bad, 0700); e != nil {
		t.Fatal(e)
	}
	if e = s.SetKeys(object(raw(map[string]any{"session": map[string]any{"bad": map[string]any{"value": 1}, "good": map[string]any{"value": 2}}}))); e == nil {
		t.Fatal("partial physical failure hidden")
	}
	data, e := os.ReadFile(filepath.Join(s.Dir, "session-good.json"))
	if e != nil || !bytes.Contains(data, []byte(`"value":2`)) {
		t.Fatal("Promise.all sibling write lost", e)
	}
}
func TestWhatsAppAuthMissingDoesNotManufactureNativeSignalCredentials(t *testing.T) {
	dir := t.TempDir()
	if _, e := OpenWhatsAppAuthStore(dir, WhatsAppAuthOptions{}); e == nil || e.Error() != "WhatsApp Signal credential initialization is not bound" {
		t.Fatal(e)
	}
	if HasWhatsAppAuth(dir) {
		t.Fatal("fake credentials persisted")
	}
	if e := os.WriteFile(filepath.Join(dir, "creds.json"), []byte(`{"registered":true,"future":9}`), 0600); e != nil {
		t.Fatal(e)
	}
	if !HasWhatsAppAuth(dir) {
		t.Fatal("old auth existence")
	}
	if e := ClearWhatsAppAuth(dir); e != nil {
		t.Fatal(e)
	}
	if HasWhatsAppAuth(dir) {
		t.Fatal("logout did not remove old auth")
	}
}
