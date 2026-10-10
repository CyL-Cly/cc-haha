package insights

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type CacheFiles interface {
	ReadFile(string) ([]byte, error)
	MkdirAll(string, fs.FileMode) error
	WriteFile(string, []byte, fs.FileMode) error
	Remove(string) error
}

type Cache struct {
	root  string
	files CacheFiles
}

func NewCache(configDir string, files CacheFiles) (*Cache, error) {
	if !filepath.IsAbs(configDir) || files == nil {
		return nil, errors.New("Insights cache requires explicit configuration directory and file owner")
	}
	return &Cache{filepath.Join(configDir, "usage-data"), files}, nil
}

func (c *Cache) DataDir() string   { return c.root }
func (c *Cache) FacetsDir() string { return filepath.Join(c.root, "facets") }
func (c *Cache) MetaDir() string   { return filepath.Join(c.root, "session-meta") }

func (c *Cache) load(path string) json.RawMessage {
	content, err := c.files.ReadFile(path)
	if err != nil {
		return nil
	}
	parsed, err := protocol.ParseAndStringifyJavaScriptJSON(protocol.DecodeJavaScriptUTF8(content))
	if err != nil {
		return nil
	}
	return parsed
}

// LoadMeta performs Source's unvalidated JSON.parse. Caller still decides
// whether a primitive, null, or an old malformed schema is usable.
func (c *Cache) LoadMeta(id string) json.RawMessage {
	return c.load(filepath.Join(c.MetaDir(), id+".json"))
}
func (c *Cache) LoadFacets(id string) json.RawMessage {
	path := filepath.Join(c.FacetsDir(), id+".json")
	value := c.load(path)
	if value == nil {
		return nil
	}
	if !ValidFacets(value) {
		_ = c.files.Remove(path)
		return nil
	}
	return value
}

func (c *Cache) save(dir, id string, value json.RawMessage) error {
	_ = c.files.MkdirAll(dir, 0777)
	parsed, err := protocol.ParseAndStringifyJavaScriptJSON(value)
	if err != nil {
		return err
	}
	var pretty bytes.Buffer
	if err = json.Indent(&pretty, parsed, "", "  "); err != nil {
		return err
	}
	return c.files.WriteFile(filepath.Join(dir, id+".json"), pretty.Bytes(), 0600)
}
func (c *Cache) SaveMeta(meta protocol.JSONObject) error {
	return c.save(c.MetaDir(), meta.String("session_id"), raw(meta))
}
func (c *Cache) SaveFacets(facets protocol.JSONObject) error {
	return c.save(c.FacetsDir(), facets.String("session_id"), raw(facets))
}
