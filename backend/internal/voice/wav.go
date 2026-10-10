package voice

import (
	"encoding/binary"
	"fmt"
)

func ValidateWAV(data []byte) (float64, error) {
	invalid := func(message string) (float64, error) { return 0, failure("voice/invalid-audio", message) }
	if len(data) > MaxAudioBytes {
		return invalid(fmt.Sprintf("Audio is too large (%d bytes, limit %d)", len(data), MaxAudioBytes))
	}
	if len(data) < 44 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return invalid("Audio is not a WAV file")
	}
	if string(data[12:16]) != "fmt " || binary.LittleEndian.Uint32(data[16:20]) != 16 || string(data[36:40]) != "data" {
		return invalid("Unsupported WAV layout; expected a canonical 44-byte header")
	}
	if binary.LittleEndian.Uint16(data[20:22]) != 1 {
		return invalid("Unsupported WAV encoding; expected PCM")
	}
	if binary.LittleEndian.Uint16(data[22:24]) != 1 {
		return invalid("Unsupported WAV channel count; expected mono")
	}
	if binary.LittleEndian.Uint32(data[24:28]) != 16000 {
		return invalid("Unsupported WAV sample rate; expected 16000 Hz")
	}
	if binary.LittleEndian.Uint16(data[34:36]) != 16 {
		return invalid("Unsupported WAV bit depth; expected 16-bit")
	}
	size := binary.LittleEndian.Uint32(data[40:44])
	if uint64(size) != uint64(len(data)-44) {
		return invalid("WAV data length does not match file length")
	}
	if size == 0 {
		return invalid("Audio is empty")
	}
	if size%2 != 0 {
		return invalid("WAV data is not a whole number of samples")
	}
	seconds := float64(size) / 32000
	if seconds > MaxAudioSeconds {
		return invalid(fmt.Sprintf("Audio is too long (%.1f s, limit 120 s)", seconds))
	}
	return seconds, nil
}
func ReadSamples(data []byte) ([]float32, error) {
	if len(data) < 44 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" || string(data[36:40]) != "data" {
		return nil, fmt.Errorf("Audio is not a canonical 44-byte-header WAV file")
	}
	size := binary.LittleEndian.Uint32(data[40:44])
	if size == 0 || size%2 != 0 || uint64(size) > uint64(len(data)-44) {
		return nil, fmt.Errorf("WAV data length is empty, odd or past the end of the file")
	}
	samples := make([]float32, size/2)
	for i := range samples {
		samples[i] = float32(int16(binary.LittleEndian.Uint16(data[44+2*i:46+2*i]))) / 32768
	}
	return samples, nil
}
