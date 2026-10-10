package imruntime

import (
	"encoding/binary"
	"fmt"
	"math"
)

type FeishuHeader struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}
type FeishuFrame struct {
	SeqID, LogID                           uint64
	Service, Method                        int32
	Headers                                []FeishuHeader
	PayloadEncoding, PayloadType, LogIDNew *string
	Payload                                []byte
}

func feishuVarint(dst []byte, v uint64) []byte { return binary.AppendUvarint(dst, v) }
func feishuProtoBytes(dst []byte, field uint64, v []byte) []byte {
	dst = feishuVarint(dst, field<<3|2)
	dst = feishuVarint(dst, uint64(len(v)))
	return append(dst, v...)
}
func EncodeFeishuFrame(f FeishuFrame) []byte {
	p := []byte{}
	for i, v := range []uint64{feishuLongBits(float64(f.SeqID)), feishuLongBits(float64(f.LogID)), uint64(int64(f.Service)), uint64(int64(f.Method))} {
		p = feishuVarint(p, uint64(i+1)<<3)
		p = feishuVarint(p, v)
	}
	for _, h := range f.Headers {
		v := feishuProtoBytes(nil, 1, []byte(h.Key))
		v = feishuProtoBytes(v, 2, []byte(h.Value))
		p = feishuProtoBytes(p, 5, v)
	}
	for _, x := range []struct {
		field uint64
		p     *string
	}{{6, f.PayloadEncoding}, {7, f.PayloadType}, {9, f.LogIDNew}} {
		if x.field == 9 {
			continue
		}
		if x.p != nil {
			p = feishuProtoBytes(p, x.field, []byte(*x.p))
		}
	}
	if f.Payload != nil {
		p = feishuProtoBytes(p, 8, f.Payload)
	}
	if f.LogIDNew != nil {
		p = feishuProtoBytes(p, 9, []byte(*f.LogIDNew))
	}
	return p
}

type feishuProtoReader struct {
	p  []byte
	at int
}

func (r *feishuProtoReader) varint() (uint64, error) {
	if r.at >= len(r.p) {
		return 0, fmt.Errorf("index out of range")
	}
	v, n := binary.Uvarint(r.p[r.at:])
	if n <= 0 {
		return 0, fmt.Errorf("invalid protobuf varint")
	}
	r.at += n
	return v, nil
}
func (r *feishuProtoReader) bytes() ([]byte, error) {
	n, e := r.varint()
	if e != nil {
		return nil, e
	}
	if n > uint64(len(r.p)-r.at) {
		return nil, fmt.Errorf("index out of range")
	}
	v := append([]byte{}, r.p[r.at:r.at+int(n)]...)
	r.at += int(n)
	return v, nil
}
func (r *feishuProtoReader) skip(wire uint64) error {
	switch wire {
	case 0:
		_, e := r.varint()
		return e
	case 1:
		if len(r.p)-r.at < 8 {
			return fmt.Errorf("index out of range")
		}
		r.at += 8
		return nil
	case 2:
		_, e := r.bytes()
		return e
	case 5:
		if len(r.p)-r.at < 4 {
			return fmt.Errorf("index out of range")
		}
		r.at += 4
		return nil
	case 3:
		for {
			tag, e := r.varint()
			if e != nil {
				return e
			}
			if tag&7 == 4 {
				return nil
			}
			if e = r.skip(tag & 7); e != nil {
				return e
			}
		}
	}
	return fmt.Errorf("invalid wire type %d", wire)
}
func DecodeFeishuFrame(p []byte) (FeishuFrame, error) {
	f := FeishuFrame{Headers: []FeishuHeader{}}
	r := feishuProtoReader{p: p}
	seen := uint8(0)
	for r.at < len(p) {
		tag, e := r.varint()
		if e != nil {
			return f, e
		}
		field, wire := tag>>3, tag&7
		if field >= 1 && field <= 4 {
			v, e := r.varint()
			if e != nil {
				return f, e
			}
			seen |= 1 << uint(field-1)
			switch field {
			case 1:
				f.SeqID = feishuRoundedLong(v)
			case 2:
				f.LogID = feishuRoundedLong(v)
			case 3:
				f.Service = int32(v)
			case 4:
				f.Method = int32(v)
			}
			continue
		}
		if field >= 5 && field <= 9 {
			v, e := r.bytes()
			if e != nil {
				return f, e
			}
			switch field {
			case 5:
				h := FeishuHeader{}
				hr := feishuProtoReader{p: v}
				hs := 0
				for hr.at < len(v) {
					t, e := hr.varint()
					if e != nil {
						return f, e
					}
					if t>>3 == 1 || t>>3 == 2 {
						x, e := hr.bytes()
						if e != nil {
							return f, e
						}
						if t>>3 == 1 {
							h.Key = string(x)
							hs |= 1
						} else {
							h.Value = string(x)
							hs |= 2
						}
					} else {
						if e = hr.skip(t & 7); e != nil {
							return f, e
						}
					}
				}
				if hs != 3 {
					return f, fmt.Errorf("missing required Header field")
				}
				f.Headers = append(f.Headers, h)
			case 6:
				s := string(v)
				f.PayloadEncoding = &s
			case 7:
				s := string(v)
				f.PayloadType = &s
			case 8:
				f.Payload = v
			case 9:
				s := string(v)
				f.LogIDNew = &s
			}
			continue
		}
		if e = r.skip(wire); e != nil {
			return f, e
		}
	}
	for i, name := range []string{"SeqID", "LogID", "service", "method"} {
		if seen&(1<<uint(i)) == 0 {
			return f, fmt.Errorf("missing required '%s'", name)
		}
	}
	return f, nil
}
func feishuInt(v float64) int32 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return int32(uint32(int64(v)))
}

// Installed protobufjs disables Long and reads/writes JavaScript Numbers.
// Preserve its double rounding and LongBits.fromNumber modulo conversion.
func feishuRoundedLong(v uint64) uint64 {
	n := float64(v)
	if n >= 18446744073709551616.0 {
		return ^uint64(0)
	}
	return uint64(n)
}
func feishuLongBits(n float64) uint64 {
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return 0
	}
	negative := n < 0
	if negative {
		n = -n
	}
	lo := uint32(math.Mod(math.Trunc(n), 4294967296))
	hi := uint32(math.Mod(math.Trunc((n-float64(lo))/4294967296), 4294967296))
	v := uint64(hi)<<32 | uint64(lo)
	if negative {
		return ^v + 1
	}
	return v
}
