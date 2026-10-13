// Package pdfread preserves the original PDF Read producer using native files
// and poppler processes. It receives no tool permission or model request state.
package pdfread

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/imageprocessing"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

const RawLimit = 20 * 1024 * 1024
const ExtractLimit = 100 * 1024 * 1024
const ExtractThreshold = 3 * 1024 * 1024
const MaxPages = 20

type Range struct{ First, Last float64 }

var integerPrefix = regexp.MustCompile(`^[+-]?[0-9]+`)

func javascriptWhitespace(value rune) bool {
	switch value {
	case '\t', '\n', '\v', '\f', '\r', ' ', '\u00a0', '\u1680', '\u2028', '\u2029', '\u202f', '\u205f', '\u3000', '\ufeff':
		return true
	}
	return value >= '\u2000' && value <= '\u200a'
}
func parseInteger(value string) float64 {
	prefix := integerPrefix.FindString(strings.TrimLeftFunc(value, javascriptWhitespace))
	if prefix == "" {
		return math.NaN()
	}
	result, err := strconv.ParseFloat(prefix, 64)
	if err != nil && !math.IsInf(result, 0) {
		return math.NaN()
	}
	return result
}
func ParseRange(pages string) *Range {
	pages = strings.TrimFunc(pages, javascriptWhitespace)
	if pages == "" {
		return nil
	}
	if strings.HasSuffix(pages, "-") {
		first := parseInteger(strings.TrimSuffix(pages, "-"))
		if math.IsNaN(first) || first < 1 {
			return nil
		}
		return &Range{first, math.Inf(1)}
	}
	firstText, lastText, hasDash := strings.Cut(pages, "-")
	first := parseInteger(firstText)
	last := first
	if hasDash {
		last = parseInteger(lastText)
	}
	if math.IsNaN(first) || math.IsNaN(last) || first < 1 || last < first {
		return nil
	}
	return &Range{first, last}
}
func ValidatePages(pages string) error {
	value := ParseRange(pages)
	if value == nil {
		return fmt.Errorf("Invalid pages parameter: \"%s\". Use formats like \"1-5\", \"3\", or \"10-20\". Pages are 1-indexed.", pages)
	}
	if math.IsInf(value.Last, 1) || value.Last-value.First+1 > MaxPages {
		return fmt.Errorf("Page range \"%s\" exceeds maximum of 20 pages per request. Please use a smaller range.", pages)
	}
	return nil
}

type ProcessResult struct {
	Code           int
	Stdout, Stderr string
}
type Files interface {
	Stat(string) (os.FileInfo, error)
	ReadFile(string) ([]byte, error)
	MkdirAll(string, os.FileMode) error
	ReadDir(string) ([]os.DirEntry, error)
}
type Options struct {
	Files            Files
	Run              func(context.Context, string, []string, time.Duration) (ProcessResult, error)
	UUID             func() string
	ResultsDirectory string
	Codec            imageprocessing.Codec
}
type Reader struct {
	options    Options
	probeCache *availabilityCache
}
type availabilityCache struct {
	mu        sync.Mutex
	available *bool
}
type Result struct {
	Data      json.RawMessage
	Blocks    json.RawMessage
	Timestamp float64
	CacheFull bool
}
type Error struct{ Reason, Message string }

func (e *Error) Error() string { return e.Message }
func New(options Options) (*Reader, error) {
	if options.Files == nil || options.Run == nil || options.UUID == nil || options.ResultsDirectory == "" {
		return nil, errors.New("PDF native files, processes and owned result directory are required")
	}
	return &Reader{options: options, probeCache: &availabilityCache{}}, nil
}

// WithResultsDirectory follows session promotion without copying a live mutex
// or resetting the process-lifetime pdftoppm availability cache.
func (r *Reader) WithResultsDirectory(directory string) *Reader {
	options := r.options
	options.ResultsDirectory = directory
	return &Reader{options: options, probeCache: r.probeCache}
}

// WithRunner snapshots each call's current owned process environment while
// retaining Source's process-lifetime pdftoppm availability decision.
func (r *Reader) WithRunner(run func(context.Context, string, []string, time.Duration) (ProcessResult, error)) *Reader {
	options := r.options
	options.Run = run
	return &Reader{options: options, probeCache: r.probeCache}
}
func (r *Reader) raw(path string) (json.RawMessage, error) {
	stats, err := r.options.Files.Stat(path)
	if err != nil {
		return nil, err
	}
	if stats.Size() == 0 {
		return nil, &Error{"empty", "PDF file is empty: " + path}
	}
	if stats.Size() > RawLimit {
		return nil, &Error{"too_large", "PDF file exceeds maximum allowed size of " + toolschema.FormatFileSize(RawLimit) + "."}
	}
	data, err := r.options.Files.ReadFile(path)
	if err != nil {
		return nil, err
	}
	valid := len(data) >= 5
	for index, expected := range []byte("%PDF-") {
		if !valid || data[index]&0x7f != expected {
			valid = false
			break
		}
	}
	if !valid {
		return nil, &Error{"corrupted", "File is not a valid PDF (missing %PDF- header): " + path}
	}
	return json.Marshal(map[string]any{"type": "pdf", "file": map[string]any{"filePath": path, "base64": base64.StdEncoding.EncodeToString(data), "originalSize": stats.Size()}})
}

var pageCount = regexp.MustCompile(`(?m)^Pages:[\t\n\v\f\r \x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]+([0-9]+)`)

func javascriptNumber(value float64) string {
	if math.IsInf(value, 1) {
		return "Infinity"
	}
	if math.IsInf(value, -1) {
		return "-Infinity"
	}
	if math.IsNaN(value) {
		return "NaN"
	}
	raw, _ := json.Marshal(value)
	return string(protocol.StringifyValidatedJavaScriptJSON(raw))
}

func (r *Reader) count(ctx context.Context, path string) (*float64, error) {
	result, err := r.options.Run(ctx, "pdfinfo", []string{path}, 10*time.Second)
	if err != nil {
		return nil, err
	}
	if result.Code != 0 {
		return nil, nil
	}
	// ECMAScript multiline anchors include CR and both Unicode line separators.
	stdout := strings.NewReplacer("\r", "\n", "\u2028", "\n", "\u2029", "\n").Replace(result.Stdout)
	match := pageCount.FindStringSubmatch(stdout)
	if match == nil {
		return nil, nil
	}
	count := parseInteger(match[1])
	if math.IsNaN(count) {
		return nil, nil
	}
	return &count, nil
}
func (r *Reader) probe(ctx context.Context) (bool, error) {
	r.probeCache.mu.Lock()
	if r.probeCache.available != nil {
		available := *r.probeCache.available
		r.probeCache.mu.Unlock()
		return available, nil
	}
	r.probeCache.mu.Unlock()
	result, err := r.options.Run(ctx, "pdftoppm", []string{"-v"}, 5*time.Second)
	if err != nil {
		return false, err
	}
	available := result.Code == 0 || result.Stderr != ""
	r.probeCache.mu.Lock()
	r.probeCache.available = &available
	r.probeCache.mu.Unlock()
	return available, nil
}
func (r *Reader) extract(ctx context.Context, path string, pages *Range) (json.RawMessage, error) {
	stats, err := r.options.Files.Stat(path)
	if err != nil {
		return nil, err
	}
	if stats.Size() == 0 {
		return nil, &Error{"empty", "PDF file is empty: " + path}
	}
	if stats.Size() > ExtractLimit {
		return nil, &Error{"too_large", "PDF file exceeds maximum allowed size for text extraction (" + toolschema.FormatFileSize(ExtractLimit) + ")."}
	}
	available, err := r.probe(ctx)
	if err != nil {
		return nil, err
	}
	if !available {
		return nil, &Error{"unavailable", "pdftoppm is not installed. Install poppler-utils (e.g. `brew install poppler` or `apt-get install poppler-utils`) to enable PDF page rendering."}
	}
	directory := filepath.Join(r.options.ResultsDirectory, "pdf-"+r.options.UUID())
	if err := r.options.Files.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	args := []string{"-jpeg", "-r", "100"}
	if pages != nil && pages.First != 0 {
		args = append(args, "-f", javascriptNumber(pages.First))
	}
	if pages != nil && pages.Last != 0 && !math.IsInf(pages.Last, 1) {
		args = append(args, "-l", javascriptNumber(pages.Last))
	}
	args = append(args, path, filepath.Join(directory, "page"))
	result, err := r.options.Run(ctx, "pdftoppm", args, 120*time.Second)
	if err != nil {
		return nil, err
	}
	if result.Code != 0 {
		text := strings.ToLower(result.Stderr)
		if strings.Contains(text, "password") {
			return nil, &Error{"password_protected", "PDF is password-protected. Please provide an unprotected version."}
		}
		if strings.Contains(text, "damaged") || strings.Contains(text, "corrupt") || strings.Contains(text, "invalid") {
			return nil, &Error{"corrupted", "PDF file is corrupted or invalid."}
		}
		return nil, &Error{"unknown", "pdftoppm failed: " + result.Stderr}
	}
	names, err := r.images(directory)
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, &Error{"corrupted", "pdftoppm produced no output pages. The PDF may be invalid."}
	}
	return json.Marshal(map[string]any{"type": "parts", "file": map[string]any{"filePath": path, "originalSize": stats.Size(), "count": len(names), "outputDir": directory}})
}
func (r *Reader) images(directory string) ([]string, error) {
	entries, err := r.options.Files.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	names := []string{}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".jpg") {
			names = append(names, entry.Name())
		}
	}
	sort.Slice(names, func(i, j int) bool {
		left, right := utf16.Encode([]rune(names[i])), utf16.Encode([]rune(names[j]))
		for index := 0; index < len(left) && index < len(right); index++ {
			if left[index] != right[index] {
				return left[index] < right[index]
			}
		}
		return len(left) < len(right)
	})
	return names, nil
}
func (r *Reader) Metadata(ctx context.Context, data json.RawMessage) (json.RawMessage, error) {
	var value protocol.JSONObject
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	file, _ := value.Object("file")
	if value.String("type") == "pdf" {
		return json.Marshal([]any{map[string]any{"type": "document", "source": map[string]any{"type": "base64", "media_type": "application/pdf", "data": file.String("base64")}}})
	}
	if value.String("type") != "parts" {
		return nil, nil
	}
	directory := file.String("outputDir")
	names, err := r.images(directory)
	if err != nil {
		return nil, err
	}
	// Source Promise.all admits every page read and keeps the sorted block order.
	// These workers belong to the enclosing native job and are joined before it
	// returns, including a page failure or cancellation.
	blocks := make([]any, len(names))
	var jobs sync.WaitGroup
	var first sync.Once
	var failure error
	for index, name := range names {
		jobs.Add(1)
		go func(index int, name string) {
			defer jobs.Done()
			err := context.Cause(ctx)
			var data []byte
			if err == nil {
				data, err = r.options.Files.ReadFile(filepath.Join(directory, name))
			}
			var output imageprocessing.Result
			if err == nil {
				output, err = imageprocessing.Normalize(r.options.Codec, data, len(data), "jpeg")
			}
			if err != nil {
				first.Do(func() { failure = err })
				return
			}
			blocks[index] = map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/" + output.MediaType, "data": base64.StdEncoding.EncodeToString(output.Buffer)}}
		}(index, name)
	}
	jobs.Wait()
	if failure != nil {
		return nil, failure
	}
	return json.Marshal(blocks)
}
func (r *Reader) Read(ctx context.Context, path string, pages *string, model string) (Result, error) {
	if err := context.Cause(ctx); err != nil {
		return Result{}, err
	}
	if pages != nil && *pages != "" {
		data, err := r.extract(ctx, path, ParseRange(*pages))
		if err != nil {
			return Result{}, err
		}
		return Result{Data: data}, nil
	}
	count, err := r.count(ctx, path)
	if err != nil {
		return Result{}, err
	}
	if count != nil && *count > 10 {
		return Result{}, fmt.Errorf("This PDF has %s pages, which is too many to read at once. Use the pages parameter to read specific page ranges (e.g., pages: \"1-5\"). Maximum 20 pages per request.", javascriptNumber(*count))
	}
	stats, err := r.options.Files.Stat(path)
	if err != nil {
		return Result{}, err
	}
	supported := !strings.Contains(strings.ToLower(model), "claude-3-haiku")
	if !supported || stats.Size() > ExtractThreshold {
		// Source attempts extraction here, records its result, then still continues
		// through full-document support/size validation. Do not substitute images.
		_, failure := r.extract(ctx, path, nil)
		if context.Cause(ctx) != nil {
			return Result{}, context.Cause(ctx)
		}
		_ = failure
	}
	if !supported {
		return Result{}, errors.New("Reading full PDFs is not supported with this model. Use a newer model (Sonnet 3.5 v2 or later), or use the pages parameter to read specific page ranges (e.g., pages: \"1-5\", maximum 20 pages per request). Page extraction requires poppler-utils: install with `brew install poppler` on macOS or `apt-get install poppler-utils` on Debian/Ubuntu.")
	}
	data, err := r.raw(path)
	if err != nil {
		return Result{}, err
	}
	return Result{Data: data, Timestamp: float64(stats.ModTime().UnixMilli()), CacheFull: true}, nil
}
