package queryhooks

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// These are the original pipe-mode TaskOutput limits, measured in JS UTF-16
// units. The native file is only output storage, never another Hook executor.
const hookMemoryLimit = 8 * 1024 * 1024
const hookDiskLimit = int64(5 * 1024 * 1024 * 1024)

type asyncTaskOutput struct {
	mu                       sync.Mutex
	stdout, stderr, output   bytes.Buffer
	pending                  [2][]byte
	recent                   []string
	units, diskUnits         int64
	memoryUnits              int
	path                     string
	file                     *os.File
	spilled, capped, cleared bool
	failure                  error
	changed                  func()
}
type asyncOutputPipe struct {
	output *asyncTaskOutput
	stderr bool
}

func newAsyncTaskOutput(changed func()) *asyncTaskOutput { return &asyncTaskOutput{changed: changed} }
func (p asyncOutputPipe) Write(data []byte) (int, error) {
	p.output.mu.Lock()
	defer p.output.mu.Unlock()
	index := 0
	if p.stderr {
		index = 1
	}
	chunk := append(p.output.pending[index], data...)
	end := 0
	for end < len(chunk) && utf8.FullRune(chunk[end:]) {
		_, size := utf8.DecodeRune(chunk[end:])
		end += size
	}
	p.output.pending[index] = append([]byte(nil), chunk[end:]...)
	p.output.write(string(protocol.DecodeJavaScriptUTF8(chunk[:end])), p.stderr)
	return len(data), nil
}
func (o *asyncTaskOutput) setPath(path string) { o.mu.Lock(); o.path = path; o.mu.Unlock() }
func (o *asyncTaskOutput) write(text string, stderr bool) {
	if o.cleared || text == "" {
		return
	}
	units := utf16.Encode([]rune(text))
	o.units += int64(len(units))
	// Original #updateProgress extracts complete, nonempty lines backwards per
	// incoming stream chunk, then appends them to a 1000-entry circular buffer.
	lines := []string{}
	extracted, pos := 0, len(units)
	for pos > 0 {
		previous := pos - 1
		for previous >= 0 && units[previous] != '\n' {
			previous--
		}
		if previous < 0 {
			break
		}
		length := pos - previous - 1
		if len(lines) < 100 && extracted < 4096 && length > 0 && length <= 4096-extracted {
			line := string(utf16.Decode(units[previous+1 : pos]))
			if strings.TrimFunc(line, javaScriptWhitespace) != "" {
				lines = append(lines, line)
				extracted += length
			}
		}
		pos = previous
	}
	for i := len(lines) - 1; i >= 0; i-- {
		o.recent = append(o.recent, lines[i])
	}
	if len(o.recent) > 1000 {
		o.recent = append([]string(nil), o.recent[len(o.recent)-1000:]...)
	}
	if o.spilled {
		if stderr {
			text = "[stderr] " + text
		}
		o.appendDisk(text)
	} else if o.memoryUnits+len(units) > hookMemoryLimit {
		if o.failure = o.spillLocked(); o.failure == nil {
			if stderr {
				text = "[stderr] " + text
			}
			o.appendDisk(text)
		}
	} else {
		if stderr {
			_, _ = o.stderr.WriteString(text)
		} else {
			_, _ = o.stdout.WriteString(text)
		}
		o.memoryUnits += len(units)
	}
	if !o.spilled {
		_, _ = o.output.WriteString(text)
	}
	if o.changed != nil {
		o.changed()
	}
}
func (o *asyncTaskOutput) appendDisk(text string) {
	if o.capped || o.file == nil {
		return
	}
	o.diskUnits += int64(len(utf16.Encode([]rune(text))))
	if o.diskUnits > hookDiskLimit {
		o.capped = true
		text = "\n[output truncated: exceeded 5GB disk cap]\n"
	}
	if _, err := o.file.WriteString(text); err != nil && o.failure == nil {
		o.failure = err
	}
}
func (o *asyncTaskOutput) spillLocked() error {
	if o.spilled {
		return o.failure
	}
	if err := os.MkdirAll(filepath.Dir(o.path), 0777); err != nil {
		return err
	}
	file, err := openHookOutput(o.path)
	if err != nil {
		return err
	}
	o.file, o.spilled = file, true
	o.appendDisk(o.stdout.String())
	if o.stderr.Len() > 0 {
		o.appendDisk("[stderr] " + o.stderr.String())
	}
	o.stdout = bytes.Buffer{}
	o.stderr = bytes.Buffer{}
	o.output = bytes.Buffer{}
	o.memoryUnits = 0
	return o.failure
}
func (o *asyncTaskOutput) spill() error { o.mu.Lock(); defer o.mu.Unlock(); return o.spillLocked() }
func (o *asyncTaskOutput) finish() {
	o.mu.Lock()
	defer o.mu.Unlock()
	for i := range o.pending {
		o.write(string(protocol.DecodeJavaScriptUTF8(o.pending[i])), i == 1)
		o.pending[i] = nil
	}
	if o.file != nil {
		if err := o.file.Close(); err != nil && o.failure == nil {
			o.failure = err
		}
		o.file = nil
	}
}
func (o *asyncTaskOutput) inMemoryStdout() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.stdout.String()
}
func (o *asyncTaskOutput) snapshot() (CommandOutput, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.spilled {
		return CommandOutput{Stdout: o.stdout.String(), Stderr: o.stderr.String(), Output: o.output.String()}, o.failure
	}
	start := len(o.recent) - 5
	if start < 0 {
		start = 0
	}
	tail := strings.Join(o.recent[start:], "\n")
	notice := fmt.Sprintf("\nOutput truncated (%dKB total). Full output saved to: %s", int64(math.Floor(float64(o.units)/1024+.5)), o.path)
	stdout := tail + notice
	if tail == "" {
		stdout = strings.TrimLeftFunc(notice, javaScriptWhitespace)
	}
	return CommandOutput{Stdout: stdout, Stderr: "", Output: stdout}, o.failure
}
func (o *asyncTaskOutput) clear() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.cleared = true
	o.stdout = bytes.Buffer{}
	o.stderr = bytes.Buffer{}
	o.output = bytes.Buffer{}
	o.recent = nil
	if o.file != nil {
		_ = o.file.Close()
		o.file = nil
	}
}
