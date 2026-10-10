package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/migrations"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/uipreferences"
	"github.com/NanmiCoder/cc-haha/backend/internal/voice"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestVoiceHTTPPreferencesAndReadinessActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun")
	}
	type row struct {
		Method string  `json:"method"`
		Path   string  `json:"path"`
		Body   *string `json:"body,omitempty"`
		WAV    *string `json:"wav,omitempty"`
	}
	body := func(s string) *string { return &s }
	wav := make([]byte, 48)
	copy(wav, "RIFF")
	copy(wav[8:12], "WAVE")
	copy(wav[12:16], "fmt ")
	binary.LittleEndian.PutUint32(wav[16:20], 16)
	binary.LittleEndian.PutUint16(wav[20:22], 1)
	binary.LittleEndian.PutUint16(wav[22:24], 1)
	binary.LittleEndian.PutUint32(wav[24:28], 16000)
	binary.LittleEndian.PutUint16(wav[34:36], 16)
	copy(wav[36:40], "data")
	binary.LittleEndian.PutUint32(wav[40:44], 4)
	encoded := base64.StdEncoding.EncodeToString(wav)
	cases := []row{{Method: "GET", Path: "/api/voice/catalog"}, {Method: "POST", Path: "/api/voice/catalog"}, {Method: "GET", Path: "/api/voice/unknown"}, {Method: "GET", Path: "/api/voice/providers/sensevoice-local/status"}, {Method: "GET", Path: "/api/voice/providers/unknown/status"}, {Method: "GET", Path: "/api/voice/providers/sensevoice-local/unknown"}, {Method: "PUT", Path: "/api/voice/preferences", Body: body(`invalid`)}, {Method: "PUT", Path: "/api/voice/preferences", Body: body(`null`)}, {Method: "PUT", Path: "/api/voice/preferences", Body: body(`[]`)}, {Method: "PUT", Path: "/api/voice/preferences", Body: body(`{"enabled":1}`)}, {Method: "PUT", Path: "/api/voice/preferences", Body: body(`{"providerId":null}`)}, {Method: "PUT", Path: "/api/voice/preferences", Body: body(`{"language":1}`)}, {Method: "PUT", Path: "/api/voice/preferences", Body: body(`{"downloadSource":"wrong"}`)}, {Method: "PUT", Path: "/api/voice/preferences", Body: body(`{"enabled":true,"language":"yue","downloadSource":"official","future":"discarded"}`)}, {Method: "GET", Path: "/api/voice/catalog"}, {Method: "POST", Path: "/api/voice/transcribe?language=wrong", WAV: &encoded}, {Method: "POST", Path: "/api/voice/transcribe?language=zh", WAV: &encoded}, {Method: "POST", Path: "/api/voice/transcribe?provider=unknown", WAV: &encoded}, {Method: "POST", Path: "/api/voice/transcribe", Body: body(`not-WAV`)}, {Method: "POST", Path: "/api/voice/providers/sensevoice-local/cancel"}, {Method: "DELETE", Path: "/api/voice/providers/sensevoice-local/assets"}}
	sourceHome := t.TempDir()
	_ = os.MkdirAll(filepath.Join(sourceHome, ".claude"), 0700)
	_ = os.MkdirAll(filepath.Join(sourceHome, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-voice-api.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = sourceHome
	cmd.Env = []string{"HOME=" + sourceHome, "USERPROFILE=" + sourceHome, "CLAUDE_CONFIG_DIR=" + filepath.Join(sourceHome, ".claude"), "TMPDIR=" + filepath.Join(sourceHome, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(sourceHome), "CC_HAHA_VOICE_API_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"homeDir": sourceHome, "cases": cases})
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var source struct {
		Rows []struct {
			Status int
			Body   json.RawMessage
		}
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.Rows) != len(cases) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(e, string(out))
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	store := storage.New(storage.Options{})
	upgrader, _ := migrations.New(migrations.Options{Store: store})
	prefs, e := uipreferences.New(uipreferences.Options{Store: store, Upgrader: upgrader, ConfigDir: func() string { return config }})
	if e != nil {
		t.Fatal(e)
	}
	provider, e := voice.NewSenseProvider(voice.SenseOptions{DataRoot: filepath.Join(home, "voice"), Environment: map[string]string{"HOME": home, "PATH": "/usr/bin:/bin"}, Platform: "darwin", Arch: "arm64"})
	if e != nil {
		t.Fatal(e)
	}
	service, e := voice.New(voice.Options{Providers: []voice.Provider{provider}, ReadPreferences: func(ctx context.Context) (protocol.JSONObject, error) {
		value, e := prefs.ReadPreferences(ctx)
		p := voiceTestObject(value.Preferences)
		return voiceTestObject(p.Get("voiceInput")), e
	}, UpdatePreferences: func(ctx context.Context, patch protocol.JSONObject) (protocol.JSONObject, error) {
		data, _ := patch.MarshalJSON()
		value, e := prefs.UpdateVoiceInputPreferences(ctx, data)
		p := voiceTestObject(value)
		return voiceTestObject(p.Get("voiceInput")), e
	}})
	if e != nil {
		t.Fatal(e)
	}
	defer service.Close(ctx)
	for i, row := range cases {
		var body []byte
		if row.Body != nil {
			body = []byte(*row.Body)
		}
		if row.WAV != nil {
			body, _ = base64.StdEncoding.DecodeString(*row.WAV)
		}
		req := httptest.NewRequest(row.Method, "http://localhost"+row.Path, bytes.NewReader(body))
		res := httptest.NewRecorder()
		if e := (&Server{}).voiceHTTP(res, req, segments(req.URL.EscapedPath()), service); e != nil {
			writeError(res, e)
		}
		var a, b any
		_ = json.Unmarshal(res.Body.Bytes(), &a)
		_ = json.Unmarshal(source.Rows[i].Body, &b)
		aa, _ := json.Marshal(a)
		bb, _ := json.Marshal(b)
		if res.Code != source.Rows[i].Status || !bytes.Equal(aa, bb) {
			t.Errorf("case%d Source%d%s Go%d%s", i, source.Rows[i].Status, bb, res.Code, aa)
		}
	}
}

func voiceTestObject(data []byte) protocol.JSONObject {
	var value protocol.JSONObject
	_ = value.UnmarshalJSON(data)
	return value
}
