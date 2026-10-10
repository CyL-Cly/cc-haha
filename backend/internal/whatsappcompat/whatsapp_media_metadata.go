package whatsappcandidate

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
)

// NodeImageThumbnail supplies the native image codec shared by the application.
// Baileys uses Sharp/Jimp rather than its socket library to produce these bytes.
type NodeImageThumbnail struct {
	JPEG          []byte
	Width, Height uint32
}

func nodeVideoThumbnail(ctx context.Context, data []byte) ([]byte, error) {
	dir, e := os.MkdirTemp("", "whatsapp-video-*")
	if e != nil {
		return nil, e
	}
	defer os.RemoveAll(dir)
	input, output := filepath.Join(dir, "input"), filepath.Join(dir, "thumbnail.jpg")
	if e = os.WriteFile(input, data, 0600); e != nil {
		return nil, e
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", "-ss", "00:00:00", "-i", input, "-y", "-vf", "scale=32:-1", "-vframes", "1", "-f", "image2", output)
	if e = cmd.Run(); e != nil {
		return nil, e
	}
	return os.ReadFile(output)
}

// NodeAudioDuration reads the same on-disk sample/timescale metadata used by
// music-metadata. Unsupported/malformed input leaves optional seconds absent.
var errNodeAudioDurationUnavailable = errors.New("audio duration unavailable")

func NodeAudioDuration(data []byte) (uint32, error) {
	duration := float64(-1)
	le := binary.LittleEndian
	be := binary.BigEndian
	if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WAVE" {
		rate, size, align, fact := uint32(0), uint32(0), uint32(0), uint32(0)
		factPresent := false
		for p := 12; p+8 <= len(data); {
			n := int(le.Uint32(data[p+4 : p+8]))
			if n < 0 || n > len(data)-p-8 {
				break
			}
			chunk := data[p+8 : p+8+n]
			switch string(data[p : p+4]) {
			case "fmt ":
				if len(chunk) >= 14 {
					rate = le.Uint32(chunk[4:8])
					align = uint32(le.Uint16(chunk[12:14]))
				}
			case "fact":
				if len(chunk) >= 4 {
					fact = le.Uint32(chunk[:4])
					factPresent = true
				}
			case "data":
				size = uint32(n)
			}
			p += 8 + n + (n & 1)
		}
		if rate > 0 && align > 0 {
			samples := float64(size) / float64(align)
			if factPresent {
				samples = float64(fact)
			}
			if samples == 0 {
				return 0, errNodeAudioDurationUnavailable
			}
			duration = samples / float64(rate)
		}
	} else if len(data) >= 4 && string(data[:4]) == "OggS" {
		var granule uint64
		var rate, skip uint32
		packet := []byte{}
		for p := 0; p+27 <= len(data) && string(data[p:p+4]) == "OggS"; {
			n := int(data[p+26])
			if p+27+n > len(data) {
				break
			}
			payload := p + 27 + n
			g := le.Uint64(data[p+6 : p+14])
			if g != ^uint64(0) && g > granule {
				granule = g
			}
			for _, length := range data[p+27 : p+27+n] {
				if payload+int(length) > len(data) {
					return 0, errors.New("truncated Ogg page")
				}
				packet = append(packet, data[payload:payload+int(length)]...)
				payload += int(length)
				if length < 255 {
					if bytes.HasPrefix(packet, []byte("OpusHead")) && len(packet) >= 12 {
						rate = 48000
						skip = uint32(le.Uint16(packet[10:12]))
					} else if len(packet) >= 16 && packet[0] == 1 && string(packet[1:7]) == "vorbis" {
						rate = le.Uint32(packet[12:16])
					} else if len(packet) >= 80 && string(packet[:8]) == "Speex   " {
						rate = le.Uint32(packet[36:40])
					} else if len(packet) >= 35 && packet[0] == 127 && string(packet[1:5]) == "FLAC" && string(packet[9:13]) == "fLaC" && packet[13]&127 == 0 {
						v := be.Uint64(packet[27:35])
						rate := v >> 44
						samples := v & 0xfffffffff
						if samples == 0 {
							return 0, errNodeAudioDurationUnavailable
						}
						if rate > 0 {
							return uint32(float64(samples) / float64(rate)), nil
						}
					}

					packet = nil
				}
			}
			p = payload
		}
		if rate > 0 && granule >= uint64(skip) {
			duration = float64(granule-uint64(skip)) / float64(rate)
		}
	} else if len(data) >= 4 && string(data[:4]) == "fLaC" {
		for p := 4; p+4 <= len(data); {
			typ := data[p] & 127
			n := int(data[p+1])<<16 | int(data[p+2])<<8 | int(data[p+3])
			if p+4+n > len(data) {
				break
			}
			if typ == 0 && n >= 18 {
				v := be.Uint64(data[p+14 : p+22])
				rate := v >> 44
				samples := v & 0xfffffffff
				if samples == 0 {
					return 0, errNodeAudioDurationUnavailable
				}
				if rate > 0 {
					duration = float64(samples) / float64(rate)
				}
				break
			}
			p += 4 + n
		}
	} else if len(data) >= 12 && string(data[4:8]) == "ftyp" {
		duration = nodeMP4Duration(data)
	} else {
		if len(data) >= 28 && string(data[:4]) == "DSD " && le.Uint64(data[20:28]) == 0 {
			return 0, errNodeAudioDurationUnavailable
		}
		duration = nodeAudioContainerDuration(data)
		if duration < 0 {
			duration = nodeMPEGDuration(data)
		}
	}
	if duration < 0 || duration > float64(^uint32(0)) {
		return 0, errors.New("unsupported or invalid audio metadata")
	}
	return uint32(duration), nil
}
func nodeMP4Duration(data []byte) float64 {
	for p := 0; p+8 <= len(data); {
		size := int(binary.BigEndian.Uint32(data[p : p+4]))
		header := 8
		if size == 1 {
			if p+16 > len(data) {
				break
			}
			wide := binary.BigEndian.Uint64(data[p+8 : p+16])
			if wide > uint64(len(data)-p) {
				break
			}
			size = int(wide)
			header = 16
		} else if size == 0 {
			size = len(data) - p
		}
		if size < header || size > len(data)-p {
			break
		}
		body := data[p+header : p+size]
		switch string(data[p+4 : p+8]) {
		case "moov", "trak", "mdia":
			if d := nodeMP4Duration(body); d >= 0 {
				return d
			}
		case "mdhd", "mvhd":
			if len(body) >= 20 {
				offset := 12
				version := body[0]
				if version == 1 {
					offset = 20
				}
				if len(body) >= offset+8 {
					scale := binary.BigEndian.Uint32(body[offset : offset+4])
					ticks := uint64(binary.BigEndian.Uint32(body[offset+4 : offset+8]))
					if version == 1 {
						if len(body) < offset+12 {
							break
						}
						ticks = binary.BigEndian.Uint64(body[offset+4 : offset+12])
					}
					if scale > 0 {
						return float64(ticks) / float64(scale)
					}
				}
			}
		}
		p += size
	}
	return -1
}
func nodeMPEGDuration(data []byte) float64 {
	p := 0
	if len(data) >= 10 && string(data[:3]) == "ID3" {
		p = 10 + int(data[6]&127)<<21 + int(data[7]&127)<<14 + int(data[8]&127)<<7 + int(data[9]&127)
	}
	duration := float64(0)
	frames := 0
	for p+7 <= len(data) {
		if data[p] == 255 && data[p+1]&0xf6 == 0xf0 { // AAC ADTS
			rates := []int{96000, 88200, 64000, 48000, 44100, 32000, 24000, 22050, 16000, 12000, 11025, 8000, 7350}
			index := int(data[p+2]>>2) & 15
			size := (int(data[p+3]&3) << 11) | (int(data[p+4]) << 3) | int(data[p+5]>>5)
			if index < len(rates) && size >= 7 && size <= len(data)-p {
				duration += float64(1024*(1+int(data[p+6]&3))) / float64(rates[index])
				frames++
				p += size
				continue
			}
		}
		if data[p] != 255 || data[p+1]&224 != 224 {
			p++
			continue
		}
		version := int(data[p+1]>>3) & 3
		layer := int(data[p+1]>>1) & 3
		bit := int(data[p+2] >> 4)
		sr := int(data[p+2]>>2) & 3
		if version == 1 || layer == 0 || bit == 0 || bit == 15 || sr == 3 {
			p++
			continue
		}
		rate := []int{44100, 48000, 32000}[sr]
		if version == 2 {
			rate /= 2
		} else if version == 0 {
			rate /= 4
		}
		tables := [][]int{{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320}, {0, 32, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 384}, {0, 32, 64, 96, 128, 160, 192, 224, 256, 288, 320, 352, 384, 416, 448}}
		bitrate := tables[layer-1][bit]
		if version != 3 {
			bitrate = []int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160}[bit]
			if layer == 3 {
				bitrate = []int{0, 32, 48, 56, 64, 80, 96, 112, 128, 144, 160, 176, 192, 224, 256}[bit]
			}
		}
		samples, coefficient := 1152, 144
		if layer == 3 {
			samples, coefficient = 384, 12
		} else if layer == 1 && version != 3 {
			samples, coefficient = 576, 72
		}
		size := coefficient*bitrate*1000/rate + int(data[p+2]>>1&1)
		if layer == 3 {
			size *= 4
		}
		if size < 4 || size > len(data)-p {
			p++
			continue
		}
		// Xing/Info frame counts exclude the metadata frame itself. The
		// installed parser returns that header duration before scanning frames.
		if frames == 0 && layer == 1 {
			side := 32
			if data[p+3]>>6 == 3 {
				side = 17
			}
			if version != 3 {
				side = 17
				if data[p+3]>>6 == 3 {
					side = 9
				}
			}
			offset := p + 4 + side
			if data[p+1]&1 == 0 {
				offset += 2
			}
			if offset+16 <= p+size {
				tag := string(data[offset : offset+4])
				flags := binary.BigEndian.Uint32(data[offset+4 : offset+8])
				if (tag == "Info" || tag == "Xing") && flags&3 == 3 {
					count := binary.BigEndian.Uint32(data[offset+8 : offset+12])
					streamSize := binary.BigEndian.Uint32(data[offset+12 : offset+16])
					if streamSize > 0 {
						return float64(count) * float64(samples) / float64(rate)
					}
				}
			}
		}
		duration += float64(samples) / float64(rate)
		frames++
		p += size
	}
	if frames == 0 {
		return -1
	}
	return duration
}
