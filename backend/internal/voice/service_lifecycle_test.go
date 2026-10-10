package voice

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestActualVoicePrepareCancelReprepareRemoveBarrierAndShutdown(t *testing.T) {
	data := bytes.Repeat([]byte("real private downloaded asset"), 8192)
	sum := sha256.Sum256(data)
	var count atomic.Int32
	started := make(chan struct{}, 4)
	canceled := make(chan struct{}, 4)
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := count.Add(1)
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		if call == 1 || call == 3 {
			_, _ = w.Write(data[:32768])
			w.(http.Flusher).Flush()
			started <- struct{}{}
			<-r.Context().Done()
			canceled <- struct{}{}
			return
		}
		offset := 0
		_, _ = fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-", &offset)
		if offset > 0 {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, len(data)-1, len(data)))
			w.Header().Set("Content-Length", strconv.Itoa(len(data)-offset))
			w.WriteHeader(206)
		}
		_, _ = w.Write(data[offset:])
	}))
	defer host.Close()
	home := t.TempDir()
	item := Item{Step: "model", Kind: "file", Target: "model.int8.onnx", Asset: Asset{Name: "model.int8.onnx", Bytes: int64(len(data)), Hash: DownloadHash{"sha256", "hex", hex.EncodeToString(sum[:])}, URLs: []string{host.URL + "/model"}}}
	provider, e := NewSenseProvider(SenseOptions{DataRoot: filepath.Join(home, "voice"), Environment: map[string]string{"HOME": home, "PATH": "/usr/bin:/bin"}, Items: []Item{item}})
	if e != nil {
		t.Fatal(e)
	}
	preferences := object([]byte(`{"enabled":false,"providerId":"sensevoice-local","language":"auto","downloadSource":"official"}`))
	s, e := New(Options{Providers: []Provider{provider}, ReadPreferences: func(context.Context) (protocol.JSONObject, error) { return preferences.Clone(), nil }, UpdatePreferences: func(_ context.Context, patch protocol.JSONObject) (protocol.JSONObject, error) {
		return preferences.Clone(), nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	t.Cleanup(func() { _ = s.Close(ctx) })
	first, e := s.Prepare(ctx, ProviderID)
	if e != nil || first.Preparation.Phase != "downloading" {
		t.Fatal(first, e)
	}
	<-started
	state, e := s.Cancel(ctx, ProviderID)
	if e != nil || state.Preparation.Phase != "cancelled" {
		t.Fatal(state, e)
	}
	second, e := s.Prepare(ctx, ProviderID)
	if e != nil || second.Preparation.Phase != "downloading" {
		t.Fatal(second, e)
	}
	select {
	case <-canceled:
	case <-ctx.Done():
		t.Fatal("old download did not finish cancellation")
	}
	awaitVoice(t, func() bool {
		state, e := s.Status(ctx, ProviderID)
		return e == nil && state.Preparation.Phase == "ready"
	})
	installed, e := os.ReadFile(filepath.Join(provider.layout.Models, "model.int8.onnx"))
	if e != nil || !bytes.Equal(installed, data) {
		t.Fatal("resumed actual file", e)
	}
	if _, e = s.RemoveAssets(ctx, ProviderID); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(provider.layout.Base); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("remove left assets", e)
	}
	if _, e = s.Prepare(ctx, ProviderID); e != nil {
		t.Fatal(e)
	}
	<-started
	removed, e := s.RemoveAssets(ctx, ProviderID)
	if e != nil || removed.Preparation.Phase != "unprepared" {
		t.Fatal("remove raced winding prepare", removed, e)
	}
	if _, e = os.Stat(provider.layout.Base); !os.IsNotExist(e) {
		t.Fatal("prepare resurrected deleted assets", e)
	}
	if e = s.Close(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Prepare(ctx, ProviderID); !errors.Is(e, context.Canceled) {
		t.Fatal("closed service restarted assets", e)
	}
}
