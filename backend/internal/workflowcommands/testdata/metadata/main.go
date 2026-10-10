package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflowcommands"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	home := os.Getenv("HOME")
	canonical, e := filepath.EvalSymlinks(home)
	if e != nil || canonical != home || os.Getenv("USERPROFILE") != home || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(home, ".claude") || os.Getenv("BUN_OPTIONS") != "--no-env-file" || os.Getenv("CC_HAHA_WORKFLOW_PARSER_FIXTURE") != "1" {
		panic("private original metadata environment required")
	}
	root, e := filepath.EvalSymlinks(os.Getenv("CC_HAHA_FIXTURE_TEMP_ROOT"))
	if e != nil || !strings.HasPrefix(home, root+string(filepath.Separator)) {
		panic("trusted private allocation required")
	}
	config := filepath.Join(home, ".claude")
	info, e := os.Lstat(config)
	if e != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		panic("private config directory required")
	}
	for _, leaf := range []string{"input.json", "result.json"} {
		if info, e := os.Lstat(filepath.Join(home, leaf)); e == nil && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
			panic("private regular leaf required")
		}
	}
	raw, e := os.ReadFile(filepath.Join(home, "input.json"))
	if e != nil {
		panic(e)
	}
	var fields []map[string]json.RawMessage
	if e = json.Unmarshal(raw, &fields); e != nil || fields == nil {
		panic("original program array required")
	}
	for _, row := range fields {
		if row == nil {
			panic("original object required")
		}
		for key := range row {
			switch key {
			case "id", "origin", "script", "renameName":
			default:
				panic("unknown original input field")
			}
		}
	}
	var rows []struct {
		ID, Origin string
		Script     json.RawMessage
		RenameName json.RawMessage
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if e = decoder.Decode(&rows); e != nil {
		panic(e)
	}
	if e = decoder.Decode(new(any)); e != io.EOF {
		panic("one original input value required")
	}
	if len(rows) > 100 {
		panic("bounded original input required")
	}
	for _, row := range rows {
		if row.ID == "" || row.Origin == "" {
			panic("original input provenance required")
		}
		units, e := protocol.DecodeJavaScriptJSONString(row.Script)
		if e != nil || workflowcommands.ScriptByteLength(units) > 700000 {
			panic("bounded original string required")
		}
		if row.RenameName != nil {
			if _, e := protocol.DecodeJavaScriptJSONString(row.RenameName); e != nil {
				panic("original rename string required")
			}
		}
	}
	result := []any{}
	owner := cooperative.New()
	defer owner.Close()
	e = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		metadata, e := workflowcommands.NewMetadata(scope, owner)
		if e != nil {
			return e
		}
		for _, row := range rows {
			u, e := protocol.DecodeJavaScriptJSONString(row.Script)
			if e != nil {
				return e
			}
			parsed, e := metadata.Parse(scope, u)
			value := map[string]any{"id": row.ID, "origin": row.Origin, "bytes": workflowcommands.ScriptByteLength(u)}
			if e != nil {
				value["capability"] = e.Error()
			} else {
				value["result"] = parsed.ResultJSON()
				name := []uint16{'r', 'e', 'n', 'a', 'm', 'e', 'd', '-', 'v', 'a', 'l', 'i', 'd'}
				if row.RenameName != nil {
					name, e = protocol.DecodeJavaScriptJSONString(row.RenameName)
					if e != nil {
						return e
					}
				}
				renamed, renameResult, renameErr := metadata.Rename(scope, u, name)
				if renameErr != nil {
					value["renameCapability"] = renameErr.Error()
				} else if renameResult.ErrorUnits != nil {
					value["rename"] = renameResult.ResultJSON()
				} else {
					value["rename"] = map[string]any{"script": json.RawMessage(protocol.QuoteJavaScriptUTF16(renamed))}
				}
				banned, bannedErr := metadata.UsesBanned(scope, u)
				if bannedErr != nil {
					value["bannedCapability"] = bannedErr.Error()
				} else {
					value["banned"] = banned
				}
				if parsed.ErrorUnits == nil {
					value["span"] = map[string]any{"startByte": parsed.StartByte, "endByte": parsed.EndByte, "endUTF16": parsed.EndUTF16}
				}
			}
			result = append(result, value)
		}
		return nil
	})
	if e != nil {
		panic(e)
	}
	out, e := json.Marshal(map[string]any{"result": result})
	if e != nil {
		panic(e)
	}
	if e = os.WriteFile(filepath.Join(home, "result.json"), out, 0600); e != nil {
		panic(e)
	}
	fmt.Println(string(out))
}
