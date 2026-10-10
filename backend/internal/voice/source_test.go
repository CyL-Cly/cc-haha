package voice

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func fixtureWAV(samples int) []byte {
	data := make([]byte, 44+samples*2)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(data)-8))
	copy(data[8:12], "WAVE")
	copy(data[12:16], "fmt ")
	binary.LittleEndian.PutUint32(data[16:20], 16)
	binary.LittleEndian.PutUint16(data[20:22], 1)
	binary.LittleEndian.PutUint16(data[22:24], 1)
	binary.LittleEndian.PutUint32(data[24:28], 16000)
	binary.LittleEndian.PutUint32(data[28:32], 32000)
	binary.LittleEndian.PutUint16(data[32:34], 2)
	binary.LittleEndian.PutUint16(data[34:36], 16)
	copy(data[36:40], "data")
	binary.LittleEndian.PutUint32(data[40:44], uint32(samples*2))
	for i := 0; i < samples; i++ {
		binary.LittleEndian.PutUint16(data[44+i*2:46+i*2], uint16(i*1000))
	}
	return data
}
func sourceVoice(t *testing.T, fixture string, input any, extra map[string]string) json.RawMessage {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun")
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0700)
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	var values map[string]any
	data, _ := json.Marshal(input)
	_ = json.Unmarshal(data, &values)
	values["homeDir"] = home
	path, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/" + fixture)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", path)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	for key, value := range extra {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	data, _ = json.Marshal(values)
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var result struct{ Capabilities struct{ Blocked []string } }
	if e = json.Unmarshal(out, &result); e != nil || len(result.Capabilities.Blocked) > 0 {
		t.Fatal("Source blocked", e, string(out))
	}
	return out
}
func compareVoice(t *testing.T, name string, actual, expected any) {
	t.Helper()
	data, _ := json.Marshal(actual)
	var a, b any
	_ = json.Unmarshal(data, &a)
	data, _ = json.Marshal(expected)
	_ = json.Unmarshal(data, &b)
	aa, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	if !bytes.Equal(aa, bb) {
		t.Errorf("%s Source%s Go%s", name, bb, aa)
	}
}
func TestVoiceAssetsWAVAndManifestActualSource(t *testing.T) {
	assets := [][3]string{{"darwin", "arm64", "auto"}, {"darwin", "x64", "official"}, {"linux", "x64", "mirror"}, {"linux", "arm64", "auto"}, {"win32", "x64", "auto"}, {"win32", "arm64", "auto"}}
	wavs := [][]byte{fixtureWAV(3), {}, fixtureWAV(0)}
	for _, offset := range []int{0, 8, 12, 16, 20, 22, 24, 34, 36, 40} {
		data := fixtureWAV(4)
		data[offset] ^= 4
		wavs = append(wavs, data)
	}
	data := fixtureWAV(3)
	wavs = append(wavs, append(data, 0))
	wavs = append(wavs, fixtureWAV(16000*121))
	encoded := []string{}
	for _, data := range wavs {
		encoded = append(encoded, base64.StdEncoding.EncodeToString(data))
	}
	manifest := `{"version":1.0,"files":[{"name":"keep","bytes":1.5,"sha256":"old","future":"<future>"},{"name":"replace","bytes":4,"sha256":"old"},{"name":null,"bytes":1,"sha256":"wrong"},{"name":"bad","bytes":null,"sha256":"wrong"},null]}`
	entries := []VerifiedFile{{Name: "replace", Bytes: 7, SHA256: "new"}, {Name: "add", Bytes: 9, SHA256: "new-add"}}
	out := sourceVoice(t, "native-voice-contract.fixture.ts", map[string]any{"assets": assets, "wav": encoded, "manifest": manifest, "entries": entries}, map[string]string{"CC_HAHA_VOICE_CONTRACT_FIXTURE": "1"})
	var source struct {
		Assets [][]Item
		WAV    []struct {
			Validated json.RawMessage
			Samples   json.RawMessage
		}
		Before, After []VerifiedFile
		Disk          json.RawMessage
	}
	if e := json.Unmarshal(out, &source); e != nil {
		t.Fatal(e)
	}
	for i, row := range assets {
		compareVoice(t, "assets", InstallItems(RuntimePlatform(row[0], row[1]), row[2]), source.Assets[i])
	}
	for i, data := range wavs {
		seconds, e := ValidateWAV(data)
		var actual any = map[string]any{"audioSeconds": seconds}
		if e != nil {
			var own *Error
			if !errors.As(e, &own) {
				t.Fatal(e)
			}
			actual = map[string]any{"message": own.Message, "code": own.Code, "status": own.Status}
		}
		compareVoice(t, "WAV validate", actual, source.WAV[i].Validated)
		samples, e := ReadSamples(data)
		first, last := []float64{}, []float64{}
		for _, v := range samples[:min(8, len(samples))] {
			first = append(first, float64(v))
		}
		for _, v := range samples[max(0, len(samples)-8):] {
			last = append(last, float64(v))
		}
		sampleBytes := make([]byte, len(samples)*4)
		for i, v := range samples {
			binary.LittleEndian.PutUint32(sampleBytes[i*4:i*4+4], math.Float32bits(v))
		}
		sum := sha256.Sum256(sampleBytes)
		actual = map[string]any{"length": len(samples), "first": first, "last": last, "sha256": hex.EncodeToString(sum[:])}
		if e != nil {
			actual = map[string]string{"message": e.Error()}
		}
		compareVoice(t, "WAV samples", actual, source.WAV[i].Samples)
	}
	path := filepath.Join(t.TempDir(), "verified.json")
	_ = os.WriteFile(path, []byte(manifest), 0600)
	compareVoice(t, "manifest read", ReadManifest(path), source.Before)
	if e := RecordVerified(path, entries); e != nil {
		t.Fatal(e)
	}
	compareVoice(t, "manifest after", ReadManifest(path), source.After)
	disk, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	compareVoice(t, "manifest atomic data", json.RawMessage(disk), source.Disk)
}
