package toolschema_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

func TestWriteCanonicalBuiltinParser(t *testing.T) {
	record, _ := parseObject(`{"kind":"builtin","name":"Write"}`)
	definition, err := toolschema.ParseDefinition(record)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := parseObject(`{"file_path":"/tmp/input.txt","content":"hello\r\n"}`)
	parsed, err := toolschema.ParseInput(definition, raw)
	if err != nil {
		t.Fatal("actual builtin Write execution parser rejects canonical input", err)
	}
	if !reflect.DeepEqual(parsed, raw) {
		t.Fatal(parsed, raw)
	}
}

func TestWriteAliasesCloneAndCanonicalize(t *testing.T) {
	record, _ := parseObject(`{"kind":"builtin","name":"Write"}`)
	definition, _ := toolschema.ParseDefinition(record)
	for _, source := range []string{`{"path":"/tmp/input.txt","file_text":""}`, `{"file_path":"/tmp/input.txt","path":"/tmp/input.txt","content":"same","file_text":"same","file_content":"same","description":"note"}`} {
		raw, _ := parseObject(source)
		before, _ := raw.MarshalJSON()
		parsed, err := toolschema.ParseInput(definition, raw)
		if err != nil {
			t.Fatal("known Write alias was refused", err)
		}
		after, _ := raw.MarshalJSON()
		if string(before) != string(after) || len(parsed.OwnProperties()) != 2 || parsed.Get("file_path") == nil || parsed.Get("content") == nil {
			t.Fatal("canonicalization mutated raw input or retained aliases", parsed)
		}
	}
}

func TestWriteInvalidInputsProduceActualValidationIssues(t *testing.T) {
	record, _ := parseObject(`{"kind":"builtin","name":"Write"}`)
	definition, _ := toolschema.ParseDefinition(record)
	for _, source := range []string{`{}`, `{"path":1,"content":"x"}`, `{"path":"/tmp/input","content":"x","file_text":"different"}`, `{"file_path":"/tmp/input","content":"x","force":true}`, `{"file_path":"/tmp/input","content":"x","description":null}`} {
		raw, _ := parseObject(source)
		_, err := toolschema.ParseInput(definition, raw)
		var validation *toolschema.ValidationError
		if !errors.As(err, &validation) || !jsonIssues(validation.Issues) {
			t.Fatalf("Write must produce structured validation issues, got %v", err)
		}
	}
}

func jsonIssues(raw []byte) bool {
	var value []json.RawMessage
	return json.Unmarshal(raw, &value) == nil && len(value) > 0
}
