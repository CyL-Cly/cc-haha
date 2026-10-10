package whatsappcandidate

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// These are physical container-header fixtures, not evidence that a decoder
// supports the corresponding audio codec. The expected optional metadata is
// produced exclusively by the actual installed music-metadata parser.
func TestNativeAudioContainerHeadersActualSource(t *testing.T) {
	for name, data := range audioContainerHeaderFixtures() {
		t.Run(name, func(t *testing.T) {
			profile := actualMediaSource(t, map[string]any{"op": "prepare", "data": base64.StdEncoding.EncodeToString(data), "mime": "audio/" + name})
			t.Log("actual Source warnings", string(profile["warnings"]))
			want := nodeObj(profile["message"])
			secondsJSON := nodeObj(want["audioMessage"])["seconds"]
			got, e := NodeAudioDuration(data)
			if len(secondsJSON) == 0 {
				if e == nil {
					t.Fatal("native generated seconds where stock Source omitted metadata", got)
				}
				return
			}
			var seconds uint32
			if err := json.Unmarshal(secondsJSON, &seconds); err != nil {
				t.Fatal(err)
			}
			if e != nil || got != seconds {
				t.Fatal("actual Source container metadata differs", got, seconds, e)
			}
		})
	}
}
func audioContainerHeaderFixtures() map[string][]byte {
	le, be := binary.LittleEndian, binary.BigEndian
	ape := make([]byte, 108)
	copy(ape, "MAC ")
	le.PutUint32(ape[4:], 3990)
	le.PutUint32(ape[8:], 52)
	le.PutUint32(ape[12:], 24)
	le.PutUint16(ape[52:], 2000)
	le.PutUint32(ape[56:], 73728)
	le.PutUint32(ape[60:], 154350)
	le.PutUint32(ape[64:], 1)
	le.PutUint16(ape[68:], 16)
	le.PutUint16(ape[70:], 1)
	le.PutUint32(ape[72:], 44100)
	copy(ape[76:], "APETAGEX")
	le.PutUint32(ape[84:], 2000)
	le.PutUint32(ape[88:], 32)
	dsf := make([]byte, 102)
	copy(dsf, "DSD ")
	le.PutUint64(dsf[4:], 28)
	le.PutUint64(dsf[12:], uint64(len(dsf)))
	le.PutUint64(dsf[20:], 92)
	copy(dsf[28:], "fmt ")
	le.PutUint64(dsf[32:], 52)
	le.PutUint32(dsf[40:], 1)
	le.PutUint32(dsf[48:], 1)
	le.PutUint32(dsf[52:], 1)
	le.PutUint32(dsf[56:], 2822400)
	le.PutUint32(dsf[60:], 1)
	le.PutUint64(dsf[64:], 9878400)
	le.PutUint32(dsf[72:], 4096)
	copy(dsf[80:], "data")
	le.PutUint64(dsf[84:], 12)
	copy(dsf[92:], "ID3")
	dsf[95] = 3
	dsfNoID3 := append([]byte{}, dsf...)
	le.PutUint64(dsfNoID3[20:], 0)
	chunk := func(tag string, body []byte) []byte {
		out := make([]byte, 12+len(body))
		copy(out, tag)
		be.PutUint64(out[4:], uint64(len(body)))
		copy(out[12:], body)
		return out
	}
	rate := make([]byte, 4)
	be.PutUint32(rate, 2822400)
	channels := make([]byte, 6)
	be.PutUint16(channels, 1)
	copy(channels[2:], "SLFT")
	props := append([]byte("SND "), chunk("FS  ", rate)...)
	props = append(props, chunk("CHNL", channels)...)
	props = append(props, chunk("CMPR", []byte{'D', 'S', 'D', ' ', 0})...)
	payload := append([]byte("DSD "), chunk("PROP", props)...)
	payload = append(payload, chunk("DSD ", make([]byte, 1234800))...)
	dff := chunk("FRM8", payload)
	sv7 := make([]byte, 384)
	copy(sv7, "MP+")
	sv7[3] = 7
	le.PutUint32(sv7[4:], 135)
	vint := func(n uint64) []byte {
		out := []byte{byte(n & 127)}
		for n >>= 7; n > 0; n >>= 7 {
			out = append([]byte{byte(n&127) | 128}, out...)
		}
		return out
	}
	body := append([]byte{0, 0, 0, 0, 8}, vint(154350)...)
	body = append(body, 0, 0, 0)
	sv8 := append([]byte("MPCKSH"), byte(3+len(body)))
	sv8 = append(sv8, body...)
	sv8 = append(sv8, 'S', 'E', 3)
	oggPage := func(packet []byte, granule uint64) []byte {
		out := make([]byte, 28+len(packet))
		copy(out, "OggS")
		out[5] = 6
		le.PutUint64(out[6:], granule)
		le.PutUint32(out[14:], 1)
		out[26] = 1
		out[27] = byte(len(packet))
		copy(out[28:], packet)
		return out
	}
	speex := make([]byte, 80)
	copy(speex, "Speex   ")
	copy(speex[8:], "1.2.1")
	le.PutUint32(speex[28:], 1)
	le.PutUint32(speex[32:], 80)
	le.PutUint32(speex[36:], 8000)
	le.PutUint32(speex[48:], 1)
	le.PutUint32(speex[52:], ^uint32(0))
	le.PutUint32(speex[56:], 160)
	le.PutUint32(speex[64:], 1)
	flac := make([]byte, 51)
	copy(flac, []byte{127, 'F', 'L', 'A', 'C', 1, 0, 0, 1})
	copy(flac[9:], "fLaC")
	flac[13] = 128
	flac[16] = 34
	be.PutUint64(flac[27:], uint64(44100)<<44|uint64(15)<<36|154350)
	opus := make([]byte, 19)
	copy(opus, "OpusHead")
	opus[8] = 1
	opus[9] = 1
	le.PutUint16(opus[10:], 312)
	le.PutUint32(opus[12:], 48000)
	vorbis := make([]byte, 30)
	vorbis[0] = 1
	copy(vorbis[1:], "vorbis")
	vorbis[11] = 1
	le.PutUint32(vorbis[12:], 44100)
	vorbis[28] = 0x86
	vorbis[29] = 1
	theora := make([]byte, 42)
	theora[0] = 128
	copy(theora[1:], "theora")
	return map[string][]byte{"ape": ape, "dsf": dsf, "dsf-no-id3": dsfNoID3, "dsdiff": dff, "musepack-sv7": sv7, "musepack-sv8": sv8, "ogg-speex": oggPage(speex, 28000), "ogg-flac": oggPage(flac, 154350), "ogg-opus": oggPage(opus, 168312), "ogg-vorbis": oggPage(vorbis, 154350), "ogg-theora": oggPage(theora, 12345)}
}

func TestNativeMPEGDurationIntegerBoundariesActualSource(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.wav")
	if e := os.WriteFile(source, mediaWAV(), 0600); e != nil {
		t.Fatal(e)
	}
	for _, duration := range []string{"0.01", "0.94", "0.95", "0.98", "1.0", "1.96", "2.0", "3.5"} {
		t.Run(duration, func(t *testing.T) {
			output := filepath.Join(dir, "voice-"+duration+".mp3")
			cmd := exec.Command("ffmpeg", "-v", "error", "-i", source, "-t", duration, "-ar", "44100", "-c:a", "libmp3lame", "-y", output)
			if out, e := cmd.CombinedOutput(); e != nil {
				t.Fatal("owned MP3 fixture", e, string(out))
			}
			data, e := os.ReadFile(output)
			if e != nil {
				t.Fatal(e)
			}
			original := actualMediaSource(t, map[string]any{"op": "prepare", "data": base64.StdEncoding.EncodeToString(data), "mime": "audio/mpeg"})
			secondsJSON := nodeObj(nodeObj(original["message"])["audioMessage"])["seconds"]
			var seconds uint32
			json.Unmarshal(secondsJSON, &seconds)
			native, e := NodeAudioDuration(data)
			if e != nil || native != seconds {
				t.Fatal("actual music-metadata integer boundary differs", native, seconds, e)
			}
		})
	}
}
