package voice

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestRealVoiceSystemTarInstallManifestAndRemoveMatchActualSource(t *testing.T) {
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	payload := []byte("actual-npm-runtime-marker")
	if e := tarWriter.WriteHeader(&tar.Header{Name: "package/sherpa-onnx.js", Mode: 0644, Size: int64(len(payload))}); e != nil {
		t.Fatal(e)
	}
	_, _ = tarWriter.Write(payload)
	_ = tarWriter.Close()
	_ = gzipWriter.Close()
	assetData := [][]byte{archive.Bytes(), []byte("original private model"), []byte("original private tokens"), []byte("original private vad")}
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		index, e := strconv.Atoi(r.URL.Path[1:])
		if e != nil || index >= len(assetData) {
			http.NotFound(w, r)
			return
		}
		data := assetData[index]
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		_, _ = w.Write(data)
	}))
	defer host.Close()
	items := []Item{}
	for i, target := range []string{"sherpa-onnx-node", "model.int8.onnx", "tokens.txt", "silero_vad.onnx"} {
		digest := sha256.Sum256(assetData[i])
		hash := DownloadHash{"sha256", "hex", hex.EncodeToString(digest[:])}
		kind, step, name, marker := "file", "model", target, ""
		if i == 0 {
			kind, step, name, marker = "package", "runtime", target+".tgz", "sherpa-onnx.js"
			digest := sha512.Sum512(assetData[i])
			hash = DownloadHash{"sha512", "base64", base64.StdEncoding.EncodeToString(digest[:])}
		}
		if i == 3 {
			step = "vad"
		}
		items = append(items, Item{step, kind, Asset{name, int64(len(assetData[i])), hash, []string{host.URL + "/" + strconv.Itoa(i)}}, target, marker})
	}
	out := sourceVoice(t, "native-voice-install.fixture.ts", map[string]any{"items": items}, map[string]string{"CC_HAHA_VOICE_INSTALL_FIXTURE": "1"})
	var source struct {
		Files          []string
		Ready, Removed bool
		Bytes          int64
		Manifest       json.RawMessage
		Final          Preparation
	}
	if e := json.Unmarshal(out, &source); e != nil {
		t.Fatal(e)
	}
	home := t.TempDir()
	layout := SenseVoiceLayout(filepath.Join(home, "voice"))
	zero := 0
	var final Preparation
	e := InstallAll(context.Background(), InstallOptions{Layout: layout, Items: items, Download: DownloadOptions{MaxRetries: &zero}, Extract: SystemTar(map[string]string{"HOME": home, "USERPROFILE": home, "PATH": "/usr/bin:/bin", "TMPDIR": home})}, func(state Preparation) { final = state })
	if e != nil {
		t.Fatal(e)
	}
	if IsInstalled(layout, items) != source.Ready || BytesOnDisk(layout, items) != source.Bytes {
		t.Fatal("readiness/progress did not reflect original files")
	}
	for i, item := range items {
		file, e := os.ReadFile(ProofPath(layout, item))
		if e != nil || base64.StdEncoding.EncodeToString(file) != source.Files[i] {
			t.Fatal("physical installed file", i, e)
		}
	}
	manifest, e := os.ReadFile(layout.Manifest)
	if e != nil {
		t.Fatal(e)
	}
	compareVoice(t, "verified manifest", json.RawMessage(manifest), source.Manifest)
	compareVoice(t, "final verify state", final, source.Final)
	if e = RemoveInstall(layout); e != nil {
		t.Fatal(e)
	}
	if IsInstalled(layout, items) != source.Removed {
		t.Fatal("assets remained installed after removal")
	}
	if _, e = os.Stat(layout.Base); !os.IsNotExist(e) {
		t.Fatal("asset directory retained", e)
	}
}
