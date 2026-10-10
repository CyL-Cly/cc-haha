package connectors

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type persistence struct {
	root, path string
	value      protocol.JSONObject
	records    map[string]protocol.JSONObject
	write      func(string, []byte) error
}

func readPersistence(root string, write func(string, []byte) error) (*persistence, error) {
	path := filepath.Join(root, "state.json")
	source, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		source = []byte(`{"schemaVersion":1,"connectors":{}}`)
	} else if err != nil {
		return nil, errors.New("Cannot read connector state; existing state was preserved")
	}
	if !json.Valid(source) {
		return nil, errors.New("Cannot read connector state; existing state was preserved")
	}
	var value protocol.JSONObject
	if value.UnmarshalJSON(source) != nil || !value.IsObject() {
		return nil, errors.New("Invalid connector state")
	}
	version := value.Get("schemaVersion")
	if version != nil {
		number, err := strconv.ParseFloat(string(version), 64)
		if err != nil || number != 0 && number != 1 {
			return nil, errors.New("Unsupported connector state version; update the app before modifying connectors")
		}
	}
	items := nested(value.Get("connectors"))
	if !items.IsObject() {
		return nil, errors.New("Invalid connector records")
	}
	value.Set("schemaVersion", raw(1))
	records := map[string]protocol.JSONObject{}
	for _, property := range items.OwnProperties() {
		item := nested(property.Value)
		if !item.IsObject() {
			return nil, errors.New("Invalid connector record")
		}
		operation := item.Get("operation")
		item.Set("operation", nil)
		item.Set("verification", nil)
		item.Set("runtime", quote("missing"))
		connection, status := "disconnected", "not-installed"
		if truthy(item.Get("installed")) {
			connection, status = "needs-auth", "disabled"
			if truthy(item.Get("enabled")) {
				status = "needs-auth"
			}
		}
		item.Set("connection", quote(connection))
		item.Set("status", quote(status))
		if truthy(operation) {
			item.Set("error", quote("Previous operation was interrupted. Check the connection again."))
		}
		records[propertyKey(property.NameJSON)] = item
		if err := items.SetPropertyJSON(property.NameJSON, raw(item)); err != nil {
			return nil, err
		}
	}
	value.Set("connectors", raw(items))
	if write == nil {
		write = writeStateAtomic
	}
	return &persistence{root: root, path: path, value: value, records: records, write: write}, nil
}

func writeStateAtomic(path string, value []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, value, 0600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func (p *persistence) get(id string) protocol.JSONObject {
	if item, ok := p.records[propertyKey(quote(id))]; ok {
		return item.Clone()
	}
	return protocol.NewJSONObject()
}

func (p *persistence) patch(id string, changes protocol.JSONObject) error {
	item := p.get(id)
	item.Assign(changes)
	items := nested(p.value.Get("connectors"))
	items.Set(id, raw(item))
	next := p.value.Clone()
	next.Set("connectors", raw(items))
	encoded, err := json.Marshal(next)
	var indented bytes.Buffer
	if err == nil {
		encoded, err = protocol.ParseAndStringifyJavaScriptJSON(encoded)
	}
	if err == nil {
		err = json.Indent(&indented, encoded, "", "  ")
	}
	if err == nil {
		err = p.write(p.path, indented.Bytes())
	}
	if err == nil {
		p.value = next
		// Source retains own undefined properties in its in-memory records;
		// stringify omits them on disk without deleting their insertion slots.
		// Re-parsing the disk projection here would reorder later assignments.
		p.records[propertyKey(quote(id))] = item
	}
	return err
}

func propertyKey(name json.RawMessage) string {
	// JSONObject has already validated these string keys. Keep their UTF-16
	// units instead of merging distinct lone-surrogate property names in UTF-8.
	canonical, _ := protocol.ParseAndStringifyJavaScriptJSON(name)
	return string(canonical)
}
