package whatsappcandidate

import (
	"bytes"
	"encoding/binary"
	"math"
)

func nodeAudioContainerDuration(data []byte) float64 {
	le, be := binary.LittleEndian, binary.BigEndian
	if len(data) >= 12 && string(data[:4]) == "FORM" && (string(data[8:12]) == "AIFF" || string(data[8:12]) == "AIFC") {
		for p := 12; p+8 <= len(data); {
			n := int(be.Uint32(data[p+4 : p+8]))
			if n > len(data)-p-8 {
				break
			}
			if string(data[p:p+4]) == "COMM" && n >= 18 {
				body := data[p+8 : p+8+n]
				exponent := int(be.Uint16(body[8:10])&0x7fff) - 16383
				rate := math.Ldexp(float64(be.Uint64(body[10:18])), exponent-63)
				if rate > 0 {
					return float64(be.Uint32(body[2:6])) / rate
				}
			}
			p += 8 + n + (n & 1)
		}
	} else if len(data) >= 32 && string(data[:4]) == "wvpk" {
		rates := []int{6000, 8000, 9600, 11025, 12000, 16000, 22050, 24000, 32000, 44100, 48000, 64000, 88200, 96000, 192000, -1}
		flags := le.Uint32(data[24:28])
		rate := rates[(flags>>23)&15]
		samples := uint64(le.Uint32(data[12:16]))
		if flags>>31 != 0 {
			samples *= 8
		}
		if rate > 0 {
			return float64(samples) / float64(rate)
		}
	} else if len(data) >= 30 && bytes.Equal(data[:16], []byte{0x30, 0x26, 0xb2, 0x75, 0x8e, 0x66, 0xcf, 0x11, 0xa6, 0xd9, 0, 0xaa, 0, 0x62, 0xce, 0x6c}) {
		properties := []byte{0xa1, 0xdc, 0xab, 0x8c, 0x47, 0xa9, 0xcf, 0x11, 0x8e, 0xe4, 0, 0xc0, 0x0c, 0x20, 0x53, 0x65}
		for p := 30; p+24 <= len(data); {
			wide := le.Uint64(data[p+16 : p+24])
			if wide < 24 || wide > uint64(len(data)-p) {
				break
			}
			if bytes.Equal(data[p:p+16], properties) && wide >= 104 {
				body := data[p+24 : p+int(wide)]
				return float64(le.Uint64(body[40:48])/1000)/10000 - float64(le.Uint64(body[56:64]))/1000
			}
			p += int(wide)
		}
	} else if len(data) >= 4 && bytes.Equal(data[:4], []byte{0x1a, 0x45, 0xdf, 0xa3}) {
		scale, duration := uint64(1000000), float64(-1)
		nodeEBMLAudioInfo(data, &scale, &duration, 0)
		if duration >= 0 {
			return duration * float64(scale) / 1e9
		}
	} else if len(data) >= 28 && string(data[:4]) == "DSD " {
		// The installed parser only reads fmt when an ID3 metadata pointer exists.
		if le.Uint64(data[20:28]) == 0 {
			return -1
		}
		for p := 28; p+12 <= len(data); {
			n := le.Uint64(data[p+4 : p+12])
			if n < 12 || n > uint64(len(data)-p) {
				break
			}
			if string(data[p:p+4]) == "fmt " && n >= 52 {
				body := data[p+12 : p+int(n)]
				rate := le.Uint32(body[16:20])
				if rate > 0 {
					return float64(le.Uint64(body[24:32])) / float64(rate)
				}
			}
			p += int(n)
		}
	} else if len(data) >= 52 && string(data[:4]) == "MAC " {
		offset := le.Uint32(data[8:12])
		if offset <= uint32(len(data)-24) {
			body := data[offset : offset+24]
			rate := le.Uint32(body[20:24])
			frames := uint64(le.Uint32(body[12:16]))
			samples := uint64(le.Uint32(body[8:12]))
			if frames > 1 {
				samples += uint64(le.Uint32(body[4:8])) * (frames - 1)
			}
			if rate > 0 {
				return float64(samples) / float64(rate)
			}
		}
	}

	// Stock parseFile cannot sniff DSDIFF from the extensionless temporary
	// original created by prepareWAMessageMedia, so optional seconds stay absent.
	if len(data) >= 16 && string(data[:4]) == "FRM8" {
		return -1
	} else if len(data) >= 24 && string(data[:3]) == "MP+" {
		frames := le.Uint32(data[4:8])
		if frames == 0 {
			return -1
		}
		rate := []int{44100, 48000, 37800, 32000}[data[10]&3]
		last := uint32(0)
		if data[23]&1 != 0 {
			last = le.Uint32(data[20:24]) >> 20 & 2047
		}
		return float64(uint64(frames-1)*1152+uint64(last)) / float64(rate)
	} else if len(data) >= 4 && string(data[:4]) == "MPCK" {
		read := func(data []byte) (uint64, int) {
			var value uint64
			for i, b := range data {
				if i >= 8 {
					return 0, 0
				}
				value = value<<7 | uint64(b&127)
				if b&128 == 0 {
					return value, i + 1
				}
			}
			return 0, 0
		}
		for p := 4; p+3 <= len(data); {
			size, n := read(data[p+2:])
			if n == 0 || size < uint64(2+n) || size > uint64(len(data)-p) {
				break
			}
			body := data[p+2+n : p+int(size)]
			if string(data[p:p+2]) == "SH" && len(body) >= 7 {
				samples, n := read(body[5:])
				if n > 0 {
					_, m := read(body[5+n:])
					if m > 0 && 5+n+m+2 <= len(body) {
						index := int(body[5+n+m] & 7)
						if index < 4 {
							return float64(samples) / float64([]int{44100, 48000, 37800, 32000}[index])
						}
					}
				}
			}
			p += int(size)
		}
	}
	return -1
}
func nodeEBMLVint(data []byte, keepMarker bool) (uint64, int, bool) {
	if len(data) == 0 || data[0] == 0 {
		return 0, 0, false
	}
	width, mask := 1, byte(128)
	for data[0]&mask == 0 {
		width++
		mask >>= 1
	}
	if width > 8 || len(data) < width {
		return 0, 0, false
	}
	value := uint64(data[0])
	if !keepMarker {
		value &= uint64(mask - 1)
	}
	for _, b := range data[1:width] {
		value = value<<8 | uint64(b)
	}
	return value, width, true
}
func nodeEBMLAudioInfo(data []byte, scale *uint64, duration *float64, depth int) {
	if depth > 8 {
		return
	}
	for p := 0; p < len(data); {
		id, n, ok := nodeEBMLVint(data[p:], true)
		if !ok {
			return
		}
		size, m, ok := nodeEBMLVint(data[p+n:], false)
		if !ok {
			return
		}
		p += n + m
		unknown := uint64(1)<<(7*m) - 1
		if size == unknown {
			size = uint64(len(data) - p)
		}
		if size > uint64(len(data)-p) {
			return
		}
		body := data[p : p+int(size)]
		switch id {
		case 0x18538067, 0x1549a966:
			nodeEBMLAudioInfo(body, scale, duration, depth+1)
		case 0x2ad7b1:
			if len(body) > 0 && len(body) <= 8 {
				var n uint64
				for _, b := range body {
					n = n<<8 | uint64(b)
				}
				*scale = n
			}
		case 0x4489:
			if len(body) == 4 {
				*duration = float64(math.Float32frombits(binary.BigEndian.Uint32(body)))
			} else if len(body) == 8 {
				*duration = math.Float64frombits(binary.BigEndian.Uint64(body))
			}
		}
		p += int(size)
	}
}
