package pluginzip

import (
	"bytes"
	"compress/flate"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const MaxFileSize = 512 * 1024 * 1024
const MaxTotalSize = 1024 * 1024 * 1024
const MaxFileCount = 100000
const MaxCompressionRatio = 50

type ZipError struct {
	Code    int
	Message string
}

func (e *ZipError) Error() string { return e.Message }
func invalidZip() error           { return &ZipError{Code: 13, Message: "invalid zip data"} }

type Entry struct {
	Name string
	Data []byte
	Mode uint16
}
type ValidationState struct {
	FileCount             int
	TotalUncompressedSize float64
	CompressedSize        float64
}
type Metadata struct {
	Name         string
	OriginalSize float64
}

var traversal = regexp.MustCompile(`(?:^|[\\/])\.\.(?:[\\/]|$)`)
var winAbsolute = regexp.MustCompile(`^[a-zA-Z]:[\\/]`)

func IsPathSafe(name, platform string) bool {
	if traversal.MatchString(name) {
		return false
	}
	if platform == "windows" || platform == "win32" {
		return !strings.HasPrefix(name, "/") && !strings.HasPrefix(name, `\`) && !winAbsolute.MatchString(name)
	}
	return !strings.HasPrefix(filepath.Clean(name), "/")
}
func ValidateFile(file Metadata, state *ValidationState, platform string) error {
	state.FileCount++
	message := ""
	if state.FileCount > MaxFileCount {
		message = fmt.Sprintf("Archive contains too many files: %d (max: %d)", state.FileCount, MaxFileCount)
	}
	if !IsPathSafe(file.Name, platform) {
		message = "Unsafe file path detected: \"" + file.Name + "\". Path traversal or absolute paths are not allowed."
	}
	size := file.OriginalSize
	if math.IsNaN(size) {
		size = 0
	}
	if size > MaxFileSize {
		message = fmt.Sprintf("File \"%s\" is too large: %.0fMB (max: 512MB)", file.Name, math.Floor(size/1024/1024+0.5))
	}
	state.TotalUncompressedSize += size
	if state.TotalUncompressedSize > MaxTotalSize {
		message = fmt.Sprintf("Archive total size is too large: %.0fMB (max: 1024MB)", math.Floor(state.TotalUncompressedSize/1024/1024+0.5))
	}
	ratio := state.TotalUncompressedSize / state.CompressedSize
	if ratio > MaxCompressionRatio {
		formatted := fmt.Sprintf("%.1f", ratio)
		if math.IsInf(ratio, 1) {
			formatted = "Infinity"
		}
		message = fmt.Sprintf("Suspicious compression ratio detected: %s:1 (max: 50:1). This may be a zip bomb.", formatted)
	}
	if message != "" {
		return errors.New(message)
	}
	return nil
}

func (s *Service) Create(ctx context.Context, sourceDir string) ([]byte, error) {
	entries := []Entry{}
	visited := map[string]bool{}
	if err := s.collect(ctx, sourceDir, "", visited, &entries); err != nil {
		return nil, err
	}
	entries = creationOrder(entries)
	return encodeZip(entries, s.options.Now())
}

func creationOrder(entries []Entry) []Entry {
	var prototype *Entry
	own := []Entry{}
	names := map[string]bool{}
	for _, entry := range entries {
		if entry.Name == "__proto__" {
			value := entry
			prototype = &value
			continue
		}
		own = append(own, entry)
		names[entry.Name] = true
	}
	// Source collects into {}, whose __proto__ setter retains the ZipEntry
	// tuple as prototype. fflate flattens with for-in, so inherited tuple keys
	// become ordinary archive entries unless shadowed by an own filename.
	if prototype != nil {
		if !names["0"] {
			own = append(own, Entry{Name: "0", Data: prototype.Data})
		}
		if !names["1"] {
			own = append(own, Entry{Name: "1/", Data: []byte{}}, Entry{Name: "1/os/", Data: []byte{}}, Entry{Name: "1/attrs/", Data: []byte{}})
		}
	}
	return ownOrder(own)
}
func (s *Service) collect(ctx context.Context, base, relative string, visited map[string]bool, entries *[]Entry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	current := base
	if relative != "" {
		current = filepath.Join(base, filepath.FromSlash(relative))
	}
	op := Operation{Kind: "readdir", Path: current}
	if err := s.before(ctx, op); err != nil {
		return nil
	}
	directory, err := os.Open(current)
	var items []os.DirEntry
	if err == nil {
		items, err = directory.ReadDir(-1)
		_ = directory.Close()
	}
	s.after(op, err)
	if err != nil {
		return nil
	}
	op = Operation{Kind: "stat", Path: current}
	if err = s.before(ctx, op); err != nil {
		return nil
	}
	info, err := os.Stat(current)
	s.after(op, err)
	if err != nil {
		return nil
	}
	if identity := directoryIdentity(current, info); identity != "" {
		if visited[identity] {
			return nil
		}
		visited[identity] = true
	}
	for _, item := range items {
		if item.Name() == ".git" {
			continue
		}
		path := filepath.Join(current, item.Name())
		name := item.Name()
		if relative != "" {
			name = relative + "/" + name
		}
		op = Operation{Kind: "lstat", Path: path}
		if err = s.before(ctx, op); err != nil {
			continue
		}
		info, err = os.Lstat(path)
		s.after(op, err)
		if err != nil {
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			op = Operation{Kind: "stat", Path: path}
			if err = s.before(ctx, op); err != nil {
				continue
			}
			info, err = os.Stat(path)
			s.after(op, err)
			if err != nil || info.IsDir() {
				continue
			}
		}
		if info.IsDir() {
			if err = s.collect(ctx, base, name, visited, entries); err != nil {
				return err
			}
		} else if info.Mode().IsRegular() {
			data, err := s.read(ctx, path)
			if err != nil {
				continue
			}
			*entries = append(*entries, Entry{Name: name, Data: data, Mode: unixMode(info)})
		}
	}
	return nil
}

// Object.entries follows integer-index key order and retains the first key's
// position when a later central-directory entry replaces its content.
func ownOrder(entries []Entry) []Entry {
	order := []Entry{}
	indexes := map[string]int{}
	for _, entry := range entries {
		if entry.Name == "__proto__" {
			continue
		}
		if i, ok := indexes[entry.Name]; ok {
			order[i] = entry
		} else {
			indexes[entry.Name] = len(order)
			order = append(order, entry)
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		ai, a := arrayIndex(order[i].Name)
		bi, b := arrayIndex(order[j].Name)
		if a && b {
			return ai < bi
		}
		return a && !b
	})
	return order
}
func arrayIndex(name string) (uint64, bool) {
	n, err := strconv.ParseUint(name, 10, 32)
	return n, err == nil && n < math.MaxUint32 && strconv.FormatUint(n, 10) == name
}

func encodeZip(entries []Entry, now time.Time) ([]byte, error) {
	if len(entries) > 0 && (now.Year() < 1980 || now.Year() > 2099) {
		return nil, &ZipError{Code: 10, Message: "date not in range 1980-2099"}
	}
	dos := uint32(now.Year()-1980)<<25 | uint32(now.Month())<<21 | uint32(now.Day())<<16 | uint32(now.Hour())<<11 | uint32(now.Minute())<<5 | uint32(now.Second()/2)
	var body, central bytes.Buffer
	for _, entry := range entries {
		name := []byte(entry.Name)
		if len(name) > 65535 {
			return nil, &ZipError{Code: 11, Message: "filename too long"}
		}
		var compressed bytes.Buffer
		writer, err := flate.NewWriter(&compressed, 6)
		if err != nil {
			return nil, err
		}
		if _, err = writer.Write(entry.Data); err != nil {
			return nil, err
		}
		if err = writer.Close(); err != nil {
			return nil, err
		}
		crc := crc32.ChecksumIEEE(entry.Data)
		offset := uint32(body.Len())
		flags := uint16(0)
		if len(name) != len(utf16.Encode([]rune(entry.Name))) {
			flags = 2048
		}
		local := make([]byte, 30)
		binary.LittleEndian.PutUint32(local, 0x04034b50)
		binary.LittleEndian.PutUint16(local[4:], 20)
		binary.LittleEndian.PutUint16(local[6:], flags)
		binary.LittleEndian.PutUint16(local[8:], 8)
		binary.LittleEndian.PutUint32(local[10:], dos)
		binary.LittleEndian.PutUint32(local[14:], crc)
		binary.LittleEndian.PutUint32(local[18:], uint32(compressed.Len()))
		binary.LittleEndian.PutUint32(local[22:], uint32(len(entry.Data)))
		binary.LittleEndian.PutUint16(local[26:], uint16(len(name)))
		body.Write(local)
		body.Write(name)
		body.Write(compressed.Bytes())
		header := make([]byte, 46)
		binary.LittleEndian.PutUint32(header, 0x02014b50)
		host := uint16(0)
		if entry.Mode != 0 {
			host = 3
		}
		binary.LittleEndian.PutUint16(header[4:], host<<8|20)
		binary.LittleEndian.PutUint16(header[6:], 20)
		binary.LittleEndian.PutUint16(header[8:], flags)
		binary.LittleEndian.PutUint16(header[10:], 8)
		binary.LittleEndian.PutUint32(header[12:], dos)
		binary.LittleEndian.PutUint32(header[16:], crc)
		binary.LittleEndian.PutUint32(header[20:], uint32(compressed.Len()))
		binary.LittleEndian.PutUint32(header[24:], uint32(len(entry.Data)))
		binary.LittleEndian.PutUint16(header[28:], uint16(len(name)))
		binary.LittleEndian.PutUint32(header[38:], uint32(entry.Mode)<<16)
		binary.LittleEndian.PutUint32(header[42:], offset)
		central.Write(header)
		central.Write(name)
	}
	end := make([]byte, 22)
	binary.LittleEndian.PutUint32(end, 0x06054b50)
	binary.LittleEndian.PutUint16(end[8:], uint16(len(entries)))
	binary.LittleEndian.PutUint16(end[10:], uint16(len(entries)))
	binary.LittleEndian.PutUint32(end[12:], uint32(central.Len()))
	binary.LittleEndian.PutUint32(end[16:], uint32(body.Len()))
	body.Write(central.Bytes())
	body.Write(end)
	return body.Bytes(), nil
}

func u16(data []byte, off int) uint16 {
	if off < 0 || off+2 > len(data) {
		return 0
	}
	return binary.LittleEndian.Uint16(data[off:])
}
func u32(data []byte, off int) uint32 {
	if off < 0 || off+4 > len(data) {
		return 0
	}
	return binary.LittleEndian.Uint32(data[off:])
}
func u64(data []byte, off int) uint64 { return uint64(u32(data, off)) | uint64(u32(data, off+4))<<32 }
func slice(data []byte, start, end int) []byte {
	if start < 0 {
		start = 0
	}
	if start > len(data) {
		start = len(data)
	}
	if end < start {
		end = start
	}
	if end > len(data) {
		end = len(data)
	}
	return data[start:end]
}
func eocd(data []byte) int {
	min := len(data) - 22 - 65535
	if min < 0 {
		min = 0
	}
	for i := len(data) - 22; i >= min; i-- {
		if u32(data, i) == 0x06054b50 {
			return i
		}
	}
	return -1
}

func ParseModes(data []byte) map[string]uint16 {
	modes := map[string]uint16{}
	end := eocd(data)
	if end < 0 {
		return modes
	}
	count, off := int(u16(data, end+10)), int(u32(data, end+16))
	for i := 0; i < count; i++ {
		if off+46 > len(data) || u32(data, off) != 0x02014b50 {
			break
		}
		nameLen, extraLen, commentLen := int(u16(data, off+28)), int(u16(data, off+30)), int(u16(data, off+32))
		name := string(protocol.DecodeJavaScriptUTF8(slice(data, off+46, off+46+nameLen)))
		if u16(data, off+4)>>8 == 3 {
			mode := uint16(u32(data, off+38) >> 16)
			if mode != 0 && name != "__proto__" {
				modes[name] = mode
			}
		}
		off += 46 + nameLen + extraLen + commentLen
	}
	return modes
}

func Unzip(data []byte, platform string) ([]Entry, error) {
	end := eocd(data)
	if end < 0 {
		return nil, invalidZip()
	}
	count := uint64(u16(data, end+8))
	if count == 0 {
		return []Entry{}, nil
	}
	off := uint64(u32(data, end+16))
	zip64 := false
	if u32(data, end-20) == 0x07064b50 {
		ze := int(u32(data, end-12))
		if u32(data, ze) == 0x06064b50 {
			zip64 = true
			count = uint64(u32(data, ze+32))
			off = uint64(u32(data, ze+48))
		}
	}
	state := ValidationState{CompressedSize: float64(len(data))}
	entries := []Entry{}
	for i := uint64(0); i < count; i++ {
		if off > uint64(len(data)) || off+46 > uint64(len(data)) {
			return nil, invalidZip()
		}
		p := int(off)
		nameLen, extraLen, commentLen := int(u16(data, p+28)), int(u16(data, p+30)), int(u16(data, p+32))
		flags := u16(data, p+8)
		method := u16(data, p+10)
		nameBytes := slice(data, p+46, p+46+nameLen)
		name := ""
		if flags&2048 != 0 {
			name = string(protocol.DecodeJavaScriptUTF8(nameBytes))
			name = strings.TrimPrefix(name, "\ufeff")
		} else {
			for _, b := range nameBytes {
				name += string(rune(b))
			}
		}
		compressed, original, local := uint64(u32(data, p+20)), uint64(u32(data, p+24)), uint64(u32(data, p+42))
		if zip64 && (compressed == math.MaxUint32 || original == math.MaxUint32 || local == math.MaxUint32) {
			found := false
			for extra := p + 46 + nameLen; extra+4 < p+46+nameLen+extraLen; extra += 4 + int(u16(data, extra+2)) {
				if u16(data, extra) == 1 {
					pos := extra + 4
					if original == math.MaxUint32 {
						original = u64(data, pos)
						pos += 8
					}
					if compressed == math.MaxUint32 {
						compressed = u64(data, pos)
						pos += 8
					}
					if local == math.MaxUint32 {
						local = u64(data, pos)
					}
					found = true
					break
				}
			}
			if !found {
				return nil, invalidZip()
			}
		}
		off += uint64(46 + nameLen + extraLen + commentLen)
		if err := ValidateFile(Metadata{Name: name, OriginalSize: float64(original)}, &state, platform); err != nil {
			return nil, err
		}
		if local > uint64(len(data)) {
			return nil, invalidZip()
		}
		start := int(local) + 30 + int(u16(data, int(local)+26)) + int(u16(data, int(local)+28))
		if compressed > uint64(math.MaxInt) {
			return nil, invalidZip()
		}
		payload := slice(data, start, start+int(compressed))
		var decoded []byte
		switch method {
		case 0:
			decoded = append([]byte(nil), payload...)
		case 8:
			var err error
			decoded, err = inflateRaw(payload, int(original))
			if err != nil {
				return nil, err
			}
		default:
			return nil, &ZipError{Code: 14, Message: fmt.Sprintf("unknown compression type %d", method)}
		}
		entries = append(entries, Entry{Name: name, Data: decoded})
	}
	return ownOrder(entries), nil
}

func (s *Service) Extract(ctx context.Context, zipPath, targetDir string) error {
	data, err := s.read(ctx, zipPath)
	if err != nil {
		return err
	}
	entries, err := Unzip(data, s.options.Platform)
	if err != nil {
		return err
	}
	modes := ParseModes(data)
	if err = s.mkdir(ctx, targetDir); err != nil {
		return err
	}
	for _, entry := range entries {
		path := filepath.Join(targetDir, filepath.FromSlash(entry.Name))
		if strings.HasSuffix(entry.Name, "/") {
			// Node join retains a final slash; mkdir("file/") differs from
			// mkdir("file") on the existing-file error path.
			path += string(filepath.Separator)
			if err = s.mkdir(ctx, path); err != nil {
				return err
			}
			continue
		}
		if err = s.mkdir(ctx, filepath.Dir(path)); err != nil {
			return err
		}
		if err = s.write(ctx, path, entry.Data); err != nil {
			return err
		}
		if mode := modes[entry.Name]; mode&0111 != 0 {
			op := Operation{Kind: "chmod", Path: path, Mode: os.FileMode(mode & 0777)}
			if err = s.before(ctx, op); err != nil {
				continue
			}
			err = os.Chmod(path, os.FileMode(mode&0777))
			s.after(op, err)
		}
	}
	return nil
}
func (s *Service) ReadAndUnzip(ctx context.Context, path string) ([]Entry, error) {
	data, err := s.read(ctx, path)
	var entries []Entry
	if err == nil {
		entries, err = Unzip(data, s.options.Platform)
	}
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return entries, err
	}
	return nil, fmt.Errorf("Failed to read or unzip file: %s", err.Error())
}
