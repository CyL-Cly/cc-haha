package sessionhistory

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type BranchError struct{ Code, Message string }

func (e *BranchError) Error() string { return e.Message }

type BranchOptions struct {
	SourceSessionID, SourceTranscriptPath, TargetMessageID        string
	Title, SourceWorkDir, SourceRepository, SourceWorktreeSession json.RawMessage
	UUID                                                          func() (string, error)
	Now                                                           func() time.Time
}

type BranchResult struct {
	SessionID                 string            `json:"sessionId"`
	Title                     json.RawMessage   `json:"title"`
	ForkPath                  string            `json:"forkPath"`
	WorkDir                   json.RawMessage   `json:"workDir"`
	SerializedMessages        []json.RawMessage `json:"serializedMessages"`
	ContentReplacementRecords []json.RawMessage `json:"contentReplacementRecords"`
}

func branchFailure(code, message string) error { return &BranchError{code, message} }

// Branch uses the CLI transcript reducer, including progress relinking,
// compaction, snips and parallel tool results. Raw rows supply persistence-only
// fields; model-facing/display projections must not become the fork source.
func (loader *Loader) Branch(o BranchOptions) (BranchResult, error) {
	if o.UUID == nil || o.Now == nil {
		return BranchResult{}, errors.New("session branch requires UUID and clock producers")
	}
	data, err := loader.options.ReadFile(o.SourceTranscriptPath)
	if os.IsNotExist(err) {
		return BranchResult{}, branchFailure("SOURCE_NOT_FOUND", "Session not found: "+o.SourceSessionID)
	}
	if err != nil {
		return BranchResult{}, err
	}
	entries := []protocol.JSONObject{}
	for _, raw := range parseLines(data) {
		entries = append(entries, object(raw))
	}
	transcript, err := loader.LoadTranscriptFile(o.SourceTranscriptPath, true)
	if err != nil {
		return BranchResult{}, err
	}
	candidates := transcript.records()
	if len(transcript.Leaves) > 0 {
		candidates = []record{}
		for _, id := range transcript.Leaves {
			if row, ok := transcript.messages[id]; ok {
				candidates = append(candidates, row)
			}
		}
	}
	var tip *record
	latest := math.Inf(-1)
	for _, row := range candidates {
		if truthy(row.value.Get("isSidechain")) {
			continue
		}
		raw := row.value.Get("timestamp")
		if len(raw) == 0 {
			continue
		}
		text, e := protocol.JavaScriptStringJSON(raw)
		if e != nil {
			return BranchResult{}, e
		}
		units, e := protocol.DecodeJavaScriptJSONString(text)
		if e != nil {
			return BranchResult{}, e
		}
		stamp, valid, e := loader.options.ParseDate(units)
		if e != nil {
			return BranchResult{}, e
		}
		if valid && stamp >= latest {
			copy := row
			tip, latest = &copy, stamp
		}
	}
	if tip == nil {
		return BranchResult{}, branchFailure("NO_BRANCHABLE_MESSAGES", "No conversation to branch")
	}
	chain, err := loader.chain(transcript, *tip)
	if err != nil {
		return BranchResult{}, err
	}
	active := []record{}
	for _, row := range chain {
		if !truthy(row.value.Get("isSidechain")) {
			active = append(active, row)
		}
	}
	if len(active) == 0 {
		return BranchResult{}, branchFailure("NO_BRANCHABLE_MESSAGES", "No conversation to branch")
	}
	target := len(active) - 1
	if o.TargetMessageID != "" {
		target = -1
		for i, row := range active {
			if row.value.String("uuid") == o.TargetMessageID {
				target = i
				break
			}
		}
	}
	if target < 0 {
		return BranchResult{}, branchFailure("INVALID_TARGET", "targetMessageId must reference a main conversation message in the active chain")
	}
	copied := active[:target+1]
	ids, resultIDs := map[string]bool{}, map[string]bool{}
	for _, row := range copied {
		ids[row.value.String("uuid")] = true
		if kind(row.value) == "user" {
			blocks, _ := array(object(row.value.Get("message")).Get("content"))
			for _, block := range blocks {
				v := object(block)
				if kind(v) == "tool_result" && isString(v.Get("tool_use_id")) {
					resultIDs[v.String("tool_use_id")] = true
				}
			}
		}
	}
	sources := map[string]protocol.JSONObject{}
	for _, row := range entries {
		k := kind(row)
		if (k == "user" || k == "assistant" || k == "attachment" || k == "system") && isString(row.Get("uuid")) && string(row.Get("isSidechain")) != "true" && ids[row.String("uuid")] {
			sources[row.String("uuid")] = row
		}
	}
	branchRows := []protocol.JSONObject{}
	for _, row := range copied {
		if source, ok := sources[row.value.String("uuid")]; ok {
			branchRows = append(branchRows, source)
		}
	}
	if len(branchRows) == 0 {
		return BranchResult{}, branchFailure("NO_BRANCHABLE_MESSAGES", "No messages to branch")
	}
	replacements := []json.RawMessage{}
	if raw, ok := transcript.MetadataValue("contentReplacements", quote(o.SourceSessionID)); ok {
		values, _ := array(raw)
		for _, row := range values {
			if resultIDs[object(row).String("toolUseId")] {
				replacements = append(replacements, row)
			}
		}
	}
	sid, err := o.UUID()
	if err != nil {
		return BranchResult{}, err
	}
	dir := filepath.Dir(o.SourceTranscriptPath)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return BranchResult{}, err
	}
	result := BranchResult{SessionID: sid, ForkPath: filepath.Join(dir, sid+".jsonl"), SerializedMessages: []json.RawMessage{}, ContentReplacementRecords: replacements}
	messageLines := []json.RawMessage{}
	parent := json.RawMessage("null")
	for _, row := range branchRows {
		fork := row.Clone()
		fork.Set("sessionId", quote(sid))
		fork.Set("parentUuid", parent)
		fork.Set("isSidechain", json.RawMessage("false"))
		fork.Set("forkedFrom", encoded(protocol.NewJSONObject(protocol.JSONField{Name: "sessionId", Value: quote(o.SourceSessionID)}, protocol.JSONField{Name: "messageUuid", Value: row.Get("uuid")})))
		messageLines = append(messageLines, encoded(fork))
		parent = row.Get("uuid")
		serialized := row.Clone()
		serialized.Set("sessionId", quote(sid))
		result.SerializedMessages = append(result.SerializedMessages, encoded(serialized))
	}
	base := o.Title
	if base == nil {
		base = quote("Branched conversation")
		for _, row := range branchRows {
			if kind(row) == "user" {
				base = branchFirstPrompt(row)
				break
			}
		}
	}
	result.Title = uniqueBranchTitle(base, dir)
	metadata := []json.RawMessage{}
	hasMeta, hasWorktree := false, false
	for _, row := range entries {
		switch kind(row) {
		case "session-meta":
			metadata = append(metadata, encoded(row))
			hasMeta = true
		case "worktree-state":
			if truthy(row.Get("worktreeSession")) {
				v := row.Clone()
				state := spread(row.Get("worktreeSession"))
				state.Set("sessionId", quote(sid))
				v.Set("sessionId", quote(sid))
				v.Set("worktreeSession", encoded(state))
				metadata = append(metadata, encoded(v))
				hasWorktree = true
			}
		case "mode", "pr-link":
			v := row.Clone()
			v.Set("sessionId", quote(sid))
			metadata = append(metadata, encoded(v))
		case "file-history-snapshot":
			id := object(row.Get("snapshot")).Get("messageId")
			if !isString(id) {
				id = row.Get("messageId")
			}
			idValue := protocol.NewJSONObject(protocol.JSONField{Name: "messageId", Value: id})
			if isString(id) && ids[idValue.String("messageId")] {
				metadata = append(metadata, encoded(row))
			}
		case "attribution-snapshot":
			if isString(row.Get("messageId")) && ids[row.String("messageId")] {
				metadata = append(metadata, encoded(row))
			}
		}
	}
	if !hasMeta && (truthy(o.SourceWorkDir) || o.SourceRepository != nil) {
		v := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("session-meta")}, protocol.JSONField{Name: "isMeta", Value: json.RawMessage("true")})
		if truthy(o.SourceWorkDir) {
			v.Set("workDir", o.SourceWorkDir)
		}
		if o.SourceRepository != nil {
			v.Set("repository", o.SourceRepository)
		}
		v.Set("timestamp", quote(o.Now().UTC().Format("2006-01-02T15:04:05.000Z")))
		metadata = append([]json.RawMessage{encoded(v)}, metadata...)
	}
	if !hasWorktree && truthy(o.SourceWorktreeSession) {
		state := spread(o.SourceWorktreeSession)
		state.Set("sessionId", quote(sid))
		v := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("worktree-state")}, protocol.JSONField{Name: "sessionId", Value: quote(sid)}, protocol.JSONField{Name: "worktreeSession", Value: encoded(state)})
		metadata = append([]json.RawMessage{encoded(v)}, metadata...)
	}
	lines := append(metadata, messageLines...)
	if len(replacements) > 0 {
		raw, _ := json.Marshal(replacements)
		lines = append(lines, encoded(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("content-replacement")}, protocol.JSONField{Name: "sessionId", Value: quote(sid)}, protocol.JSONField{Name: "replacements", Value: raw})))
	}
	lines = append(lines, encoded(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("custom-title")}, protocol.JSONField{Name: "sessionId", Value: quote(sid)}, protocol.JSONField{Name: "customTitle", Value: result.Title})))
	var out strings.Builder
	for _, raw := range lines {
		out.Write(protocol.StringifyValidatedJavaScriptJSON(raw))
		out.WriteByte('\n')
	}
	if err = os.WriteFile(result.ForkPath, []byte(out.String()), 0600); err != nil {
		return BranchResult{}, err
	}
	result.WorkDir = json.RawMessage("null")
	for _, raw := range metadata {
		v := object(raw)
		if kind(v) == "session-meta" {
			if x := v.Get("workDir"); len(x) > 0 && !isNull(x) {
				result.WorkDir = x
			}
			break
		}
	}
	if isNull(result.WorkDir) && len(o.SourceWorkDir) > 0 && !isNull(o.SourceWorkDir) {
		result.WorkDir = o.SourceWorkDir
	}
	if isNull(result.WorkDir) {
		for i := len(branchRows) - 1; i >= 0; i-- {
			if raw := branchRows[i].Get("cwd"); isString(raw) {
				result.WorkDir = raw
				break
			}
		}
	}
	return result, nil
}

func isString(raw json.RawMessage) bool { return len(raw) > 0 && raw[0] == '"' }

func branchFirstPrompt(row protocol.JSONObject) json.RawMessage {
	raw := object(row.Get("message")).Get("content")
	if !isString(raw) {
		blocks, _ := array(raw)
		raw = nil
		for _, block := range blocks {
			v := object(block)
			if kind(v) == "text" {
				raw = v.Get("text")
				break
			}
		}
	}
	units, err := protocol.DecodeJavaScriptJSONString(raw)
	if err != nil {
		return quote("Branched conversation")
	}
	// JS whitespace and slice count UTF-16 units, including a split surrogate.
	compact := []uint16{}
	pending := false
	for _, unit := range units {
		if protocol.IsJavaScriptWhitespace(rune(unit)) {
			pending = len(compact) > 0
			continue
		}
		if pending {
			compact = append(compact, ' ')
			pending = false
		}
		compact = append(compact, unit)
	}
	if len(compact) > 100 {
		compact = compact[:100]
	}
	if len(compact) == 0 {
		return quote("Branched conversation")
	}
	return protocol.QuoteJavaScriptUTF16(compact)
}

func uniqueBranchTitle(base json.RawMessage, dir string) json.RawMessage {
	units, _ := protocol.DecodeJavaScriptJSONString(base)
	titles := map[string]bool{}
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		rows := parseLines(data)
		for i := len(rows) - 1; i >= 0; i-- {
			row := object(rows[i])
			raw := row.Get("customTitle")
			if kind(row) == "custom-title" && isString(raw) {
				title, _ := protocol.DecodeJavaScriptJSONString(raw)
				start, stop := 0, len(title)
				for start < stop && protocol.IsJavaScriptWhitespace(rune(title[start])) {
					start++
				}
				for stop > start && protocol.IsJavaScriptWhitespace(rune(title[stop-1])) {
					stop--
				}
				if stop > start {
					titles[key(protocol.QuoteJavaScriptUTF16(title[start:stop]), -1)] = true
				}
				break
			}
		}
	}
	for number := 1; ; number++ {
		suffix := " (Branch)"
		if number > 1 {
			suffix = " (Branch " + strconv.Itoa(number) + ")"
		}
		candidate := protocol.QuoteJavaScriptUTF16(append(append([]uint16(nil), units...), utf16.Encode([]rune(suffix))...))
		if !titles[key(candidate, -1)] {
			// Source counts padded decimal suffixes as the same used number.
			if number > 1 {
				used := false
				for encodedTitle := range titles {
					prefix := key(protocol.QuoteJavaScriptUTF16(append(append([]uint16(nil), units...), utf16.Encode([]rune(" (Branch "))...)), -1)
					if !strings.HasPrefix(encodedTitle, prefix) {
						continue
					}
					rest := []byte(strings.TrimPrefix(encodedTitle, prefix))
					if len(rest) < 4 || len(rest)%2 != 0 {
						continue
					}
					u := make([]uint16, len(rest)/2)
					for i := range u {
						u[i] = uint16(rest[2*i]) | uint16(rest[2*i+1])<<8
					}
					if u[len(u)-1] != ')' {
						continue
					}
					digits := string(utf16.Decode(u[:len(u)-1]))
					if digits == "" || strings.Trim(digits, "0123456789") != "" {
						continue
					}
					n, e := strconv.ParseInt(digits, 10, 64)
					if e == nil && n == int64(number) {
						used = true
						break
					}
				}
				if used {
					continue
				}
			}
			return candidate
		}
	}
}
