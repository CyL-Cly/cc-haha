package builtintools

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/nativepaths"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (s *Service) notebookPath(in protocol.JSONObject) (string, error) {
	p, err := text(in.Get("notebook_path"))
	if err != nil {
		return "", err
	}
	if strings.ContainsRune(p, 0) {
		return "", errors.New("Path contains null bytes")
	}
	if s.options.Platform != "windows" && (strings.HasPrefix(p, `\\`) || strings.HasPrefix(p, "//")) {
		return "", capability("UNC network paths")
	}
	if s.options.Platform == "windows" {
		if !nativepaths.IsWindowsAbsolute(p) {
			return nativepaths.ResolveWindows(p, s.options.CWD), nil
		}
		return p, nil
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(s.options.CWD, p)
		return filepath.Clean(p), nil
	}
	// Source preserves an already absolute notebook_path for its cache key and
	// result. A prior Read normalizes its own path independently.
	return p, nil
}

func notebookValidation(message string, code int) error {
	return &builtinwrite.ValidationError{Message: message, ErrorCode: code}
}

func notebookCells(notebook protocol.JSONObject) ([]protocol.JSONObject, error) {
	var cells []protocol.JSONObject
	if value := notebook.Get("cells"); value == nil {
		return nil, errors.New("undefined is not an object (evaluating 'notebook.cells.findIndex')")
	} else if string(value) == "null" {
		return nil, errors.New("null is not an object (evaluating 'notebook.cells.findIndex')")
	} else if len(value) == 0 || value[0] != '[' {
		return nil, errors.New("notebook.cells.findIndex is not a function")
	}
	if err := json.Unmarshal(notebook.Get("cells"), &cells); err != nil {
		return nil, capability("Notebook malformed cells property access")
	}
	return cells, nil
}
func notebookIndex(cells []protocol.JSONObject, id json.RawMessage) int {
	for index, cell := range cells {
		if cell.Get("id") != nil && jsonSame(cell.Get("id"), id) {
			return index
		}
	}
	var name string
	_ = json.Unmarshal(id, &name)
	if regexp.MustCompile(`^cell-[0-9]+$`).MatchString(name) {
		n, err := strconv.ParseUint(name[5:], 10, 64)
		if err == nil && n <= math.MaxInt {
			return int(n)
		}
		return math.MaxInt
	}
	return -1
}
func (s *Service) validateNotebook(in protocol.JSONObject, scope *cooperative.Scope) error {
	p, err := s.notebookPath(in)
	if err != nil {
		return err
	}
	if s.options.Platform == "windows" && nativepaths.IsUNC(p) {
		return nil
	}
	if filepath.Ext(p) != ".ipynb" {
		return notebookValidation("File must be a Jupyter notebook (.ipynb file). For editing other file types, use the FileEdit tool.", 2)
	}
	mode := in.String("edit_mode")
	if in.Get("edit_mode") == nil {
		mode = "replace"
	}
	if mode != "replace" && mode != "insert" && mode != "delete" {
		return notebookValidation("Edit mode must be replace, insert, or delete.", 4)
	}
	if mode == "insert" && !jsonTruthy(in.Get("cell_type")) {
		return notebookValidation("Cell type is required when using edit_mode=insert.", 5)
	}
	state, ok, err := s.options.Cache.Get(scope, p)
	if err != nil {
		return err
	}
	if !ok {
		return notebookValidation("File has not been read yet. Read it first before writing to it.", 9)
	}
	stamp, err := mtime(p)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if stamp > state.Timestamp {
		return notebookValidation("File has been modified since read, either by the user or by a linter. Read it again before attempting to write it.", 10)
	}
	metadata, err := editMetadata(p)
	if err != nil {
		return err
	}
	if !metadata.Exists {
		return notebookValidation("Notebook file does not exist.", 1)
	}
	// safeParseJSON, used only at validation, strips BOM. call deliberately
	// uses fresh JSON.parse and does not share or mutate a memoized object.
	content := strings.TrimPrefix(metadata.Content, "\ufeff")
	if !json.Valid([]byte(content)) || !jsonTruthy([]byte(content)) {
		return notebookValidation("Notebook is not valid JSON.", 6)
	}
	id := in.Get("cell_id")
	if !jsonTruthy(id) {
		if mode != "insert" {
			return notebookValidation("Cell ID must be specified when not inserting a new cell.", 7)
		}
		return nil
	}
	var notebook protocol.JSONObject
	if err = notebook.UnmarshalJSON([]byte(content)); err != nil || !notebook.IsObject() {
		return capability("Notebook nonobject cells property access")
	}
	cells, err := notebookCells(notebook)
	if err != nil {
		return err
	}
	index := notebookIndex(cells, id)
	if index >= 0 && index < len(cells) {
		return nil
	}
	var name string
	_ = json.Unmarshal(id, &name)
	if regexp.MustCompile(`^cell-[0-9]+$`).MatchString(name) {
		return notebookValidation(fmt.Sprintf("Cell with index %d does not exist in notebook.", index), 7)
	}
	var contentText taskText
	contentText.add("Cell with ID \"")
	contentText.value(id)
	contentText.add("\" not found in notebook.")
	return notebookValidation(string(utf16.Decode(contentText)), 8)
}

// notebookCellID is the shortest radix-36 fraction whose rounding interval
// contains the actual Math.random double, followed by Source substring(2,15).
// No UUID or separate pseudo-random identity replaces that producer.
func notebookCellID(value float64) (string, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value >= 1 {
		return "", capability("Notebook Math.random producer value")
	}
	if value == 0 {
		return "", nil
	}
	exact := new(big.Rat).SetFloat64(value)
	lower := new(big.Rat).Add(exact, new(big.Rat).SetFloat64(math.Nextafter(value, 0)))
	lower.Quo(lower, big.NewRat(2, 1))
	upper := new(big.Rat).Add(exact, new(big.Rat).SetFloat64(math.Nextafter(value, 1)))
	upper.Quo(upper, big.NewRat(2, 1))
	scale := big.NewInt(1)
	for count := 1; count <= 400; count++ {
		scale.Mul(scale, big.NewInt(36))
		scaled := new(big.Rat).Mul(exact, new(big.Rat).SetInt(scale))
		floor := new(big.Int).Quo(scaled.Num(), scaled.Denom())
		fraction := new(big.Rat).Sub(scaled, new(big.Rat).SetInt(floor))
		candidate := new(big.Int).Set(floor)
		if fraction.Cmp(big.NewRat(1, 2)) >= 0 {
			candidate.Add(candidate, big.NewInt(1))
		}
		chosen := new(big.Rat).SetFrac(candidate, new(big.Int).Set(scale))
		if chosen.Cmp(lower) >= 0 && chosen.Cmp(upper) <= 0 {
			digits := candidate.Text(36)
			if len(digits) < count {
				digits = strings.Repeat("0", count-len(digits)) + digits
			}
			if len(digits) > 13 {
				digits = digits[:13]
			}
			return digits, nil
		}
	}
	return "", capability("Notebook radix-36 number conversion")
}
func notebookFailure(in protocol.JSONObject, p, message string) json.RawMessage {
	typeValue := in.Get("cell_type")
	if typeValue == nil {
		typeValue = quote("code")
	}
	return object(protocol.JSONField{Name: "new_source", Value: in.Get("new_source")}, protocol.JSONField{Name: "cell_type", Value: typeValue}, field("language", "python"), field("edit_mode", "replace"), field("error", message), protocol.JSONField{Name: "cell_id", Value: in.Get("cell_id")}, field("notebook_path", p), field("original_file", ""), field("updated_file", ""))
}
func notebookPretty(notebook protocol.JSONObject) (string, error) {
	encoded, err := notebook.MarshalJSON()
	if err != nil {
		return "", err
	}
	encoded, err = protocol.ParseAndStringifyJavaScriptJSON(encoded)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	if err = json.Indent(&out, encoded, "", " "); err != nil {
		return "", err
	}
	return out.String(), nil
}
func (s *Service) notebookEdit(ctx context.Context, in protocol.JSONObject, scope *cooperative.Scope) (json.RawMessage, error) {
	return s.notebookEditWithParent(ctx, in, scope, nil)
}
func (s *Service) notebookEditWithParent(ctx context.Context, in protocol.JSONObject, scope *cooperative.Scope, parent json.RawMessage) (json.RawMessage, error) {
	p, err := s.notebookPath(in)
	if err != nil {
		return nil, err
	}
	disabledHistory := taskEnvironmentTruthy(s.options.Environment["CLAUDE_CODE_DISABLE_FILE_CHECKPOINTING"])
	if !disabledHistory && (!s.options.NonInteractive || taskEnvironmentTruthy(s.options.Environment["CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"])) {
		if s.options.FileHistoryTrack == nil {
			return nil, capability("enabled actual fileHistoryTrackEdit for NotebookEdit")
		}
		if err = s.options.FileHistoryTrack(scope, p, parent); err != nil {
			return nil, err
		}
	}
	if err = context.Cause(ctx); err != nil {
		return nil, err
	}
	metadata, err := editMetadata(p)
	if err != nil {
		return notebookFailure(in, p, err.Error()), nil
	}
	if !metadata.Exists {
		return notebookFailure(in, p, fmt.Sprintf("ENOENT: no such file or directory, open '%s'", p)), nil
	}
	if !json.Valid([]byte(metadata.Content)) {
		return notebookFailure(in, p, "Notebook is not valid JSON."), nil
	}
	var notebook protocol.JSONObject
	if err = notebook.UnmarshalJSON([]byte(metadata.Content)); err != nil || !notebook.IsObject() {
		return nil, capability("Notebook nonobject call property access")
	}
	cells, err := notebookCells(notebook)
	if err != nil {
		return notebookFailure(in, p, err.Error()), nil
	}
	id := in.Get("cell_id")
	index := 0
	if jsonTruthy(id) {
		index = notebookIndex(cells, id)
		if in.String("edit_mode") == "insert" {
			index++
		}
	}
	mode := in.String("edit_mode")
	cellType := in.Get("cell_type")
	if mode == "replace" && index == len(cells) {
		mode = "insert"
		if cellType == nil {
			cellType = quote("code")
		}
	}
	var metadataObject protocol.JSONObject
	if notebook.Get("metadata") == nil {
		return notebookFailure(in, p, "undefined is not an object (evaluating 'notebook.metadata.language_info')"), nil
	}
	if string(notebook.Get("metadata")) == "null" {
		return notebookFailure(in, p, "null is not an object (evaluating 'notebook.metadata.language_info')"), nil
	}
	_ = metadataObject.UnmarshalJSON(notebook.Get("metadata"))
	var languageInfo protocol.JSONObject
	_ = languageInfo.UnmarshalJSON(metadataObject.Get("language_info"))
	language := languageInfo.Get("name")
	if language == nil || string(language) == "null" {
		language = quote("python")
	}
	var major, minor float64
	_ = json.Unmarshal(notebook.Get("nbformat"), &major)
	_ = json.Unmarshal(notebook.Get("nbformat_minor"), &minor)
	var newID json.RawMessage
	if major > 4 || major == 4 && minor >= 5 {
		if mode == "insert" {
			if s.options.Random == nil {
				return nil, capability("Notebook actual Math.random producer")
			}
			generated, err := notebookCellID(s.options.Random())
			if err != nil {
				return nil, err
			}
			newID = quote(generated)
		} else {
			newID = id
		}
	}
	if mode == "delete" {
		if index < 0 {
			index = len(cells) + index
			if index < 0 {
				index = 0
			}
		}
		if index < len(cells) {
			cells = append(cells[:index], cells[index+1:]...)
		}
	} else if mode == "insert" {
		if index < 0 {
			index = len(cells) + index
			if index < 0 {
				index = 0
			}
		}
		if index > len(cells) {
			index = len(cells)
		}
		kind := "code"
		if string(cellType) == `"markdown"` {
			kind = "markdown"
		}
		cell := protocol.NewJSONObject(field("cell_type", kind), protocol.JSONField{Name: "id", Value: newID}, protocol.JSONField{Name: "source", Value: in.Get("new_source")}, field("metadata", map[string]any{}))
		if kind == "code" {
			cell.Set("execution_count", []byte("null"))
			cell.Set("outputs", []byte("[]"))
		}
		cells = append(cells, protocol.JSONObject{})
		copy(cells[index+1:], cells[index:len(cells)-1])
		cells[index] = cell
	} else {
		if index < 0 || index >= len(cells) {
			return notebookFailure(in, p, "undefined is not an object (evaluating 'targetCell.source = new_source')"), nil
		}
		cell := cells[index]
		cell.Set("source", in.Get("new_source"))
		if cell.String("cell_type") == "code" {
			cell.Set("execution_count", []byte("null"))
			cell.Set("outputs", []byte("[]"))
		}
		if cellType != nil && !jsonSame(cellType, cell.Get("cell_type")) {
			cell.Set("cell_type", cellType)
		}
		cells[index] = cell
	}
	notebook.Set("cells", raw(cells))
	updated, err := notebookPretty(notebook)
	if err != nil {
		return nil, err
	}
	toWrite := updated
	if metadata.Endings == "CRLF" {
		toWrite = strings.ReplaceAll(updated, "\n", "\r\n")
	}
	data := []byte(toWrite)
	if metadata.Encoding == "utf16le" {
		units := utf16.Encode([]rune(toWrite))
		data = make([]byte, len(units)*2)
		for index, unit := range units {
			binary.LittleEndian.PutUint16(data[index*2:], unit)
		}
	}
	if err = (builtinwrite.OSFiles{}).WriteFlush(resolved(p), data); err != nil {
		return notebookFailure(in, p, err.Error()), nil
	}
	stamp, err := mtime(p)
	if err != nil {
		return notebookFailure(in, p, err.Error()), nil
	}
	if err = s.options.Cache.Set(scope, p, builtinwrite.FileState{Content: quote(updated), Timestamp: stamp}); err != nil {
		return nil, err
	}
	if cellType == nil {
		cellType = quote("code")
	}
	if mode == "" {
		mode = "replace"
	}
	if !jsonTruthy(newID) {
		newID = nil
	}
	return object(protocol.JSONField{Name: "new_source", Value: in.Get("new_source")}, protocol.JSONField{Name: "cell_type", Value: cellType}, protocol.JSONField{Name: "language", Value: language}, field("edit_mode", mode), protocol.JSONField{Name: "cell_id", Value: newID}, field("error", ""), field("notebook_path", p), field("original_file", metadata.Content), field("updated_file", updated)), nil
}
func mapNotebookEdit(result protocol.JSONObject, id json.RawMessage) (protocol.JSONObject, error) {
	var content taskText
	if jsonTruthy(result.Get("error")) {
		out := protocol.NewJSONObject(protocol.JSONField{Name: "tool_use_id", Value: id}, field("type", "tool_result"), protocol.JSONField{Name: "content", Value: result.Get("error")}, field("is_error", true))
		return out, nil
	}
	value := result.Get("cell_id")
	if value == nil {
		value = quote("undefined")
	}
	switch result.String("edit_mode") {
	case "replace":
		content.add("Updated cell ")
		content.value(value)
		content.add(" with ")
		content.value(result.Get("new_source"))
	case "insert":
		content.add("Inserted cell ")
		content.value(value)
		content.add(" with ")
		content.value(result.Get("new_source"))
	case "delete":
		content.add("Deleted cell ")
		content.value(value)
	default:
		content.add("Unknown edit mode")
	}
	return protocol.NewJSONObject(protocol.JSONField{Name: "tool_use_id", Value: id}, field("type", "tool_result"), protocol.JSONField{Name: "content", Value: protocol.QuoteJavaScriptUTF16(content)}), nil
}
