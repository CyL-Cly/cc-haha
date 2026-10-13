package localindex

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
	"github.com/dlclark/regexp2/v2"
)

const ContentParserVersion int64 = 1
const ContentMaxLineBytes = 16 * 1024 * 1024
const ContentBatchBytes = 4 * 1024 * 1024
const ContentLineTooLarge = "SEARCH_CONTENT_JSONL_LINE_TOO_LARGE"

type ContentCandidate struct {
	Path                string  `json:"path"`
	ProjectPath         string  `json:"projectPath"`
	OwnerSessionID      string  `json:"ownerSessionId"`
	OwnerTranscriptPath string  `json:"ownerTranscriptPath"`
	ModifiedAtMs        float64 `json:"modifiedAtMs"`
}
type SearchableSegment struct {
	Role string `json:"role"`
	Text string `json:"text"`
}
type ContentProjectResult struct {
	Kind          string `json:"kind"`
	Action        string `json:"action,omitempty"`
	State         string `json:"state,omitempty"`
	IndexedBytes  int64  `json:"indexedBytes"`
	IndexedLines  int64  `json:"indexedLines"`
	DocumentCount int64  `json:"documentCount"`
	Reason        string `json:"reason,omitempty"`
}

func (r ContentProjectResult) MarshalJSON() ([]byte, error) {
	if r.Kind != "indexed" {
		m := map[string]any{"kind": r.Kind}
		if r.Reason != "" {
			m["reason"] = r.Reason
		}
		return json.Marshal(m)
	}
	type alias ContentProjectResult
	return json.Marshal(alias(r))
}

type ContentProjectorOptions struct {
	Index           *ContentIndex
	Budget          *sessionstore.Budget
	SpoolDir        string
	ParserVersion   int64
	MaxLineBytes    int
	Now             func() time.Time
	Verify          func(context.Context, string, Fingerprint) SourceChange
	OnBatch         func(int, int)
	OnCommitStarted func()
}
type ContentProjector struct {
	o ContentProjectorOptions
}

func NewContentProjector(o ContentProjectorOptions) (*ContentProjector, error) {
	if o.Index == nil || o.Budget == nil {
		return nil, errors.New("Search content projection requires its shared index and history budget")
	}
	if o.SpoolDir == "" {
		o.SpoolDir = os.TempDir()
	}
	if !filepath.IsAbs(o.SpoolDir) {
		return nil, errors.New("Search content spool requires an absolute directory")
	}
	if o.ParserVersion == 0 {
		o.ParserVersion = ContentParserVersion
	}
	if o.MaxLineBytes <= 0 {
		o.MaxLineBytes = ContentMaxLineBytes
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Verify == nil {
		o.Verify = VerifyFingerprint
	}
	return &ContentProjector{o: o}, nil
}

var xmlBlocks = regexp2.MustCompile(`<([a-z][\w-]*)(?:\s[^>]*)?>[\s\S]*?</\1>\s*`, regexp2.ECMAScript|regexp2.IgnoreCase)
var metadataTags = []string{"command-name", "command-message", "command-args", "local-command-caveat", "skill-format"}

func hasMetadata(s string) bool {
	for _, t := range metadataTags {
		if strings.Contains(s, "<"+t+">") {
			return true
		}
	}
	return false
}
func commandDisplay(s string) *string {
	if !hasMetadata(s) {
		return nil
	}
	rest, err := xmlBlocks.ReplaceFunc(s, func(m regexp2.Match) string {
		tag := strings.ToLower(m.GroupByNumber(1).String())
		for _, t := range metadataTags {
			if tag == t {
				return ""
			}
		}
		return m.String()
	}, -1, -1)
	if err != nil || trimJS(rest) != "" {
		return nil
	}
	readTag := func(tag string) string {
		rx := regexp2.MustCompile(`<`+tag+`(?:\s[^>]*)?>([\s\S]*?)</`+tag+`>`, regexp2.ECMAScript|regexp2.IgnoreCase)
		m, _ := rx.FindStringMatch(s)
		if m == nil {
			return ""
		}
		v := trimJS(m.GroupByNumber(1).String())
		for _, p := range [][2]string{{"&lt;", "<"}, {"&gt;", ">"}, {"&quot;", `"`}, {"&#39;", "'"}, {"&amp;", "&"}} {
			v = strings.ReplaceAll(v, p[0], p[1])
		}
		return v
	}
	name := readTag("command-name")
	if name == "" {
		return nil
	}
	args := readTag("command-args")
	var result string
	if readTag("skill-format") == "true" {
		message := readTag("command-message")
		if message == "" {
			message = strings.TrimPrefix(name, "/")
		}
		result = "Skill(" + message + ")"
	} else {
		if !strings.HasPrefix(name, "/") {
			name = "/" + name
		}
		result = name
		if trimmed := trimJS(args); trimmed != "" {
			result += " " + trimmed
		}
	}
	return &result
}
func textBlocks(content json.RawMessage, trimStrings bool) []string {
	if len(content) == 0 {
		return nil
	}
	var s string
	if json.Unmarshal(content, &s) == nil {
		if trimStrings {
			s = trimJS(s)
		}
		return []string{s}
	}
	var blocks []json.RawMessage
	if json.Unmarshal(content, &blocks) != nil {
		return nil
	}
	out := []string{}
	for _, raw := range blocks {
		var block protocol.JSONObject
		if json.Unmarshal(raw, &block) != nil || block.String("type") != "text" {
			continue
		}
		if json.Unmarshal(block.Get("text"), &s) != nil {
			continue
		}
		s = trimJS(s)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
func ExtractSearchableSegments(entry protocol.JSONObject) []SearchableSegment {
	kind := entry.String("type")
	if kind != "user" && kind != "assistant" {
		return []SearchableSegment{}
	}
	var message protocol.JSONObject
	if json.Unmarshal(entry.Get("message"), &message) != nil {
		return []SearchableSegment{}
	}
	content := message.Get("content")
	metadataTexts := textBlocks(content, false)
	all := len(metadataTexts) > 0
	displays := []string{}
	hide := false
	for _, text := range metadataTexts {
		display := commandDisplay(trimJS(text))
		if display == nil {
			all = false
		} else {
			displays = append(displays, *display)
		}
		hide = hide || hasMetadata(text)
	}
	if all {
		return []SearchableSegment{{Role: "user", Text: strings.Join(displays, "\n")}}
	}
	if hide {
		return []SearchableSegment{}
	}
	role := "user"
	if kind == "assistant" || message.String("role") == "assistant" {
		role = "assistant"
	}
	out := []SearchableSegment{}
	for _, text := range textBlocks(content, true) {
		if text != "" {
			out = append(out, SearchableSegment{role, text})
		}
	}
	return out
}
func validUTF8(data []byte) []byte {
	if utf8.Valid(data) {
		return data
	}
	var out bytes.Buffer
	for len(data) > 0 {
		r, n := utf8.DecodeRune(data)
		out.WriteRune(r)
		data = data[n:]
	}
	return out.Bytes()
}
func parseContentLine(line []byte, start, number int64) []ContentDocument {
	end := len(line) - 1
	if end > 0 && line[end-1] == '\r' {
		end--
	}
	var entry protocol.JSONObject
	if json.Unmarshal(validUTF8(line[:end]), &entry) != nil {
		return nil
	}
	uuid := nullableValue(entry.Get("uuid"))
	if len(entry.Get("uuid")) > 0 && entry.Get("uuid")[0] != '"' {
		uuid = nil
	}
	stamp := nullableValue(entry.Get("timestamp"))
	if len(entry.Get("timestamp")) > 0 && entry.Get("timestamp")[0] != '"' {
		stamp = nil
	}
	out := []ContentDocument{}
	for n, s := range ExtractSearchableSegments(entry) {
		out = append(out, ContentDocument{number, start, int64(len(line)), int64(n), s.Role, uuid, stamp, s.Text, NormalizeContent(s.Text)})
	}
	return out
}

type reduction struct {
	documentCount, indexedBytes, indexedLines int64
	lineTooLarge                              bool
}

func readContentLines(ctx context.Context, f *os.File, start, end, startingLine int64, maxLine int, batch func([]ContentDocument, int) error) (reduction, error) {
	result := reduction{indexedBytes: start, indexedLines: startingLine}
	reader := bufio.NewReaderSize(io.NewSectionReader(f, start, end-start), 256*1024)
	line := []byte{}
	docs := []ContentDocument{}
	batchBytes := 0
	flush := func() error {
		if len(docs) == 0 {
			return ctx.Err()
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := batch(docs, batchBytes); err != nil {
			return err
		}
		result.documentCount += int64(len(docs))
		docs = nil
		batchBytes = 0
		return ctx.Err()
	}
	for {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		part, err := reader.ReadSlice('\n')
		if len(line)+len(part) > maxLine {
			result.lineTooLarge = true
			return result, flush()
		}
		line = append(line, part...)
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			if err != io.EOF {
				return result, err
			}
			return result, flush()
		}
		result.indexedLines++
		for _, d := range parseContentLine(line, result.indexedBytes, result.indexedLines) {
			bytes := len(d.Body) + len(d.NormalizedBody)
			if len(docs) > 0 && (batchBytes+bytes > ContentBatchBytes || len(docs) >= 256) {
				if e := flush(); e != nil {
					return result, e
				}
			}
			docs = append(docs, d)
			batchBytes += bytes
			if batchBytes >= ContentBatchBytes {
				if e := flush(); e != nil {
					return result, e
				}
			}
		}
		result.indexedBytes += int64(len(line))
		line = nil
	}
}
func (p *ContentProjector) Project(ctx context.Context, c ContentCandidate) (ContentProjectResult, error) {
	release, e := searchProjectionBudget.acquire(ctx)
	if e != nil {
		return ContentProjectResult{}, e
	}
	defer release()
	var result ContentProjectResult
	err := p.o.Budget.With(ctx, sessionstore.MetadataLane, func() error { var e error; result, e = p.project(ctx, c); return e })
	return result, err
}
func (p *ContentProjector) Delete(ctx context.Context, path string) (ContentProjectResult, error) {
	release, e := searchProjectionBudget.acquire(ctx)
	if e != nil {
		return ContentProjectResult{}, e
	}
	defer release()
	err := p.o.Index.DeleteSource(ctx, path)
	return ContentProjectResult{Kind: "deleted"}, err
}
func resultChange(c SourceChange) ContentProjectResult {
	return ContentProjectResult{Kind: c.Kind, Reason: c.Reason}
}
func (p *ContentProjector) project(ctx context.Context, c ContentCandidate) (ContentProjectResult, error) {
	if err := ctx.Err(); err != nil {
		return ContentProjectResult{}, err
	}
	old, err := p.o.Index.GetSource(ctx, c.Path)
	if err != nil {
		return ContentProjectResult{}, err
	}
	action := "full"
	start, startingLine := int64(0), int64(0)
	if old != nil {
		action = "rebuild"
		previous := DeserializeFingerprint(old.Fingerprint)
		change := SourceChange{Kind: "rebuild", Reason: "rewrite"}
		if previous != nil {
			change = DetectSourceChange(ctx, c.Path, *previous, p.o.ParserVersion)
		}
		if change.Kind == "retry" {
			return resultChange(change), nil
		}
		if change.Kind == "deleted" {
			err := p.o.Index.DeleteSource(ctx, c.Path)
			return ContentProjectResult{Kind: "deleted"}, err
		}
		if change.Kind == "unchanged" {
			if old.ProjectPath != c.ProjectPath || old.OwnerSessionID != c.OwnerSessionID || old.OwnerTranscriptPath != c.OwnerTranscriptPath || old.ModifiedAtMs != c.ModifiedAtMs {
				old.ProjectPath = c.ProjectPath
				old.OwnerSessionID = c.OwnerSessionID
				old.OwnerTranscriptPath = c.OwnerTranscriptPath
				old.ModifiedAtMs = c.ModifiedAtMs
				old.UpdatedAtMs = p.o.Now().UnixMilli()
				if err = p.o.Index.ApplySource(ctx, *old, true, nil); err != nil {
					return ContentProjectResult{}, err
				}
			}
			return ContentProjectResult{Kind: "indexed", Action: "unchanged", State: old.State, IndexedBytes: old.IndexedBytes, IndexedLines: old.IndexedLines}, nil
		}
		if change.Kind == "append" {
			action = "append"
			start = change.ReadFrom
			startingLine = old.IndexedLines
		}
	}
	reduced, source, err := p.projectToSpool(ctx, c, action, start, startingLine)
	if err != nil {
		if ctx.Err() != nil {
			return ContentProjectResult{}, ctx.Err()
		}
		if os.IsNotExist(err) {
			return p.Delete(ctx, c.Path)
		}
		return resultChange(retryChange(err)), nil
	}
	if source == nil {
		return reduced, nil
	}
	return reduced, nil
}
func (p *ContentProjector) projectToSpool(ctx context.Context, c ContentCandidate, action string, start, startingLine int64) (ContentProjectResult, *ContentSource, error) {
	before, err := CaptureFingerprint(ctx, c.Path, start, p.o.ParserVersion)
	if err != nil {
		return ContentProjectResult{}, nil, err
	}
	file, err := os.Open(c.Path)
	if err != nil {
		return ContentProjectResult{}, nil, err
	}
	defer file.Close()
	held, err := fileSnapshot(file)
	if err != nil {
		return ContentProjectResult{}, nil, err
	}
	if !sameSnapshot(held, FileSnapshot{before.Size, before.MtimeMs, before.CtimeMs, before.FileIdentity}) {
		return resultChange(SourceChange{Kind: "retry", Reason: "changed-during-read"}), nil, nil
	}
	directory, err := os.MkdirTemp(p.o.SpoolDir, "claude-search-spool-")
	if err != nil {
		return ContentProjectResult{}, nil, err
	}
	defer os.RemoveAll(directory)
	spool, err := sql.Open("sqlite", filepath.Join(directory, "documents.sqlite"))
	if err != nil {
		return ContentProjectResult{}, nil, err
	}
	defer spool.Close()
	spool.SetMaxOpenConns(1)
	if _, err = spool.ExecContext(ctx, `PRAGMA journal_mode=OFF;PRAGMA cache_size=-512;PRAGMA temp_store=FILE;CREATE TABLE documents(seq INTEGER PRIMARY KEY,jsonlLine INTEGER,byteStart INTEGER,byteLength INTEGER,segmentIndex INTEGER,role TEXT,messageId TEXT,timestamp TEXT,body TEXT,normalizedBody TEXT)`); err != nil {
		return ContentProjectResult{}, nil, err
	}
	reduction, err := readContentLines(ctx, file, start, before.Size, startingLine, p.o.MaxLineBytes, func(documents []ContentDocument, size int) error {
		if p.o.OnBatch != nil {
			p.o.OnBatch(len(documents), size)
		}
		tx, e := spool.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		for _, d := range documents {
			if _, e = tx.ExecContext(ctx, `INSERT INTO documents(jsonlLine,byteStart,byteLength,segmentIndex,role,messageId,timestamp,body,normalizedBody) VALUES(?,?,?,?,?,?,?,?,?)`, d.JSONLLine, d.ByteStart, d.ByteLength, d.SegmentIndex, d.Role, d.MessageID, d.Timestamp, d.Body, d.NormalizedBody); e != nil {
				return e
			}
		}
		return tx.Commit()
	})
	if err != nil {
		return ContentProjectResult{}, nil, err
	}
	after, err := fileSnapshot(file)
	if err != nil {
		return ContentProjectResult{}, nil, err
	}
	if !sameSnapshot(held, after) {
		return resultChange(SourceChange{Kind: "retry", Reason: "changed-during-read"}), nil, nil
	}
	commit, err := CaptureFingerprint(ctx, c.Path, reduction.indexedBytes, p.o.ParserVersion)
	if err != nil {
		return ContentProjectResult{}, nil, err
	}
	if !sameSnapshot(FileSnapshot{before.Size, before.MtimeMs, before.CtimeMs, before.FileIdentity}, FileSnapshot{commit.Size, commit.MtimeMs, commit.CtimeMs, commit.FileIdentity}) || before.FirstWindowHash != commit.FirstWindowHash || before.LastWindowHash != commit.LastWindowHash {
		return resultChange(SourceChange{Kind: "retry", Reason: "changed-during-read"}), nil, nil
	}
	verified := p.o.Verify(ctx, c.Path, *commit)
	if verified.Kind != "unchanged" {
		if verified.Kind != "retry" {
			verified = SourceChange{Kind: "retry", Reason: "changed-during-read"}
		}
		return resultChange(verified), nil, nil
	}
	if err = ctx.Err(); err != nil {
		return ContentProjectResult{}, nil, err
	}
	state := "ready"
	var errorCode *string
	if reduction.lineTooLarge {
		state = "degraded"
		s := ContentLineTooLarge
		errorCode = &s
	} else if reduction.indexedBytes < commit.Size {
		state = "pending"
	}
	source := ContentSource{c.Path, c.ProjectPath, c.OwnerSessionID, c.OwnerTranscriptPath, c.ModifiedAtMs, commit.Size, commit.MtimeMs, commit.FileIdentity, SerializeFingerprint(*commit), reduction.indexedBytes, reduction.indexedLines, p.o.ParserVersion, state, errorCode, p.o.Now().UnixMilli()}
	err = p.o.Index.ApplySource(ctx, source, action == "append", func(yield func(ContentDocument) error) error {
		rows, e := spool.QueryContext(ctx, `SELECT jsonlLine,byteStart,byteLength,segmentIndex,role,messageId,timestamp,body,normalizedBody FROM documents ORDER BY seq`)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var d ContentDocument
			if e = rows.Scan(&d.JSONLLine, &d.ByteStart, &d.ByteLength, &d.SegmentIndex, &d.Role, &d.MessageID, &d.Timestamp, &d.Body, &d.NormalizedBody); e != nil {
				return e
			}
			if e = yield(d); e != nil {
				return e
			}
		}
		return rows.Err()
	}, func() (err error) {
		defer func() {
			if reason := recover(); reason != nil {
				if e, ok := reason.(error); ok {
					err = e
				} else {
					err = fmt.Errorf("%v", reason)
				}
			}
		}()
		if p.o.OnCommitStarted != nil {
			p.o.OnCommitStarted()
		}
		return context.Cause(ctx)
	})
	if err != nil {
		return ContentProjectResult{}, nil, err
	}
	return ContentProjectResult{Kind: "indexed", Action: action, State: state, IndexedBytes: reduction.indexedBytes, IndexedLines: reduction.indexedLines, DocumentCount: reduction.documentCount}, &source, nil
}
