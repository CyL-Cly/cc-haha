package agentsapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"go.yaml.in/yaml/v3"
)

type parsedFile struct {
	document *yaml.Node
	body     string
	identity os.FileInfo
}

var frontmatter = regexp.MustCompile("^---[\t ]*\r?\n([\\s\\S]*?)\r?\n---[\t ]*(?:\r?\n|$)")

func fileStat(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fail(403, "READ_ONLY_AGENT", "Only regular Markdown agent files are editable")
	}
	return info, nil
}
func matchingIdentity(current, expected os.FileInfo) error {
	milliseconds := func(info os.FileInfo) float64 {
		return float64(info.ModTime().Unix())*1000 + float64(info.ModTime().Nanosecond())/1000000
	}
	if !os.SameFile(current, expected) || current.Size() != expected.Size() || milliseconds(current) != milliseconds(expected) {
		return fail(409, "CONFLICT", "Agent target changed while the update was in progress")
	}
	return nil
}
func assertFileIdentity(path string, expected os.FileInfo) error {
	current, err := fileStat(path)
	if err != nil {
		return err
	}
	if current == nil {
		return fail(404, "NOT_FOUND", "Agent not found: "+strings.TrimSuffix(filepath.Base(path), ".md"))
	}
	return matchingIdentity(current, expected)
}
func readFile(path string) (*parsedFile, error) {
	info, err := fileStat(path)
	if err != nil || info == nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err = assertFileIdentity(path, info); err != nil {
		return nil, err
	}
	text := string(protocol.DecodeJavaScriptUTF8(raw))
	indexes := frontmatter.FindStringSubmatchIndex(text)
	if indexes == nil {
		return nil, bad("Agent file must contain YAML frontmatter")
	}
	decoder := yaml.NewDecoder(strings.NewReader(text[indexes[2]:indexes[3]]))
	var document yaml.Node
	if err = decoder.Decode(&document); err != nil || len(document.Content) == 0 {
		return nil, bad("Agent file contains invalid YAML frontmatter")
	}
	var extra yaml.Node
	if err = decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, bad("Agent file contains invalid YAML frontmatter")
	}
	var value any
	if err = document.Decode(&value); err != nil {
		return nil, bad("Agent file contains invalid YAML frontmatter")
	}
	node := document.Content[0]
	if node.Kind != yaml.MappingNode {
		return nil, bad("Agent frontmatter must be a YAML mapping")
	}
	return &parsedFile{&document, text[indexes[1]:], info}, nil
}
func mapping(document *yaml.Node) *yaml.Node {
	if document.Kind == yaml.DocumentNode {
		return document.Content[0]
	}
	return document
}
func yamlField(document *yaml.Node, key string) *yaml.Node {
	node := mapping(document)
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}
func yamlSet(document *yaml.Node, key string, value json.RawMessage) error {
	root := mapping(document)
	index := -1
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Value == key {
			index = i
			break
		}
	}
	if bytes.Equal(value, []byte("null")) {
		if index >= 0 {
			root.Content = append(root.Content[:index], root.Content[index+2:]...)
		}
		return nil
	}
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	var node yaml.Node
	switch x := decoded.(type) {
	case json.Number:
		tag := "!!int"
		if strings.ContainsAny(string(x), ".eE") {
			tag = "!!float"
		}
		node = yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: string(x)}
	default:
		if err := node.Encode(decoded); err != nil {
			return err
		}
	}
	if index >= 0 {
		old := root.Content[index+1]
		node.HeadComment, node.LineComment, node.FootComment = old.HeadComment, old.LineComment, old.FootComment
		if old.Kind == node.Kind {
			node.Style = old.Style
		}
		if old.Kind == yaml.ScalarNode && node.Kind == yaml.ScalarNode {
			node.Anchor = old.Anchor
			*old = node
		} else {
			root.Content[index+1] = &node
		}
	} else {
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, &node)
	}
	return nil
}
func identityName(parsed *parsedFile, name string) error {
	field := yamlField(parsed.document, "name")
	var value any
	if field != nil {
		_ = field.Decode(&value)
	}
	if value != name {
		return fail(409, "CONFLICT", "Agent target identity does not match requested agent: "+name)
	}
	return nil
}
func parsedFields(parsed *parsedFile, name string) protocol.JSONObject {
	out := protocol.NewJSONObject()
	out.Set("name", quote(name))
	for _, key := range []string{"description", "model", "effort", "tools", "color"} {
		node := yamlField(parsed.document, key)
		if node == nil {
			continue
		}
		var value any
		if node.Decode(&value) != nil {
			continue
		}
		valid := false
		switch key {
		case "effort":
			switch value.(type) {
			case string, int, int64, uint64, float64:
				valid = true
			}
		case "tools":
			if rows, ok := value.([]any); ok {
				filtered := []string{}
				for _, row := range rows {
					if text, ok := row.(string); ok {
						filtered = append(filtered, text)
					}
				}
				value = filtered
				valid = true
			}
		default:
			_, valid = value.(string)
		}
		if valid {
			raw, err := json.Marshal(value)
			if err == nil {
				out.Set(key, raw)
			}
		}
	}
	if text := trim(parsed.body); text != "" {
		out.Set("systemPrompt", quote(text))
	}
	return out
}
func applyFields(document *yaml.Node, fields protocol.JSONObject) error {
	for _, name := range []string{"description", "model", "effort", "tools", "color"} {
		if raw := fields.Get(name); raw != nil {
			if err := yamlSet(document, name, raw); err != nil {
				return err
			}
		}
	}
	return nil
}
func render(document *yaml.Node, body string) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(document); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return []byte("---\n" + strings.TrimRight(buffer.String(), " \t\r\n") + "\n---\n" + body), nil
}
func temporaryPath(path, suffix string) (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+"."+hex.EncodeToString(random[:])+suffix), nil
}
func writeTemporary(path string, raw []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, err = file.Write(raw)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	return err
}
func atomicCreate(path string, raw []byte) error {
	temporary, err := temporaryPath(path, ".tmp")
	if err != nil {
		return err
	}
	defer os.Remove(temporary)
	if err = writeTemporary(temporary, raw, 0600); err != nil {
		return err
	}
	if err = os.Link(temporary, path); os.IsExist(err) {
		return fail(409, "CONFLICT", "Agent already exists: "+strings.TrimSuffix(filepath.Base(path), ".md"))
	}
	return err
}
func atomicReplace(path string, raw []byte, expected os.FileInfo) error {
	current, err := fileStat(path)
	if err != nil {
		return err
	}
	if current == nil {
		return fail(404, "NOT_FOUND", "Agent not found: "+strings.TrimSuffix(filepath.Base(path), ".md"))
	}
	if err = matchingIdentity(current, expected); err != nil {
		return err
	}
	temporary, err := temporaryPath(path, ".tmp")
	if err != nil {
		return err
	}
	defer os.Remove(temporary)
	if err = writeTemporary(temporary, raw, current.Mode()); err != nil {
		return err
	}
	if err = assertFileIdentity(path, expected); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}
func (s *Service) GetFile(ctx context.Context, name string, mutation MutationContext) (*MutationResult, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	directory, path, err := s.resolveTarget(ctx, name, mutation)
	if err != nil {
		return nil, err
	}
	parsed, err := readFile(path)
	if err != nil || parsed == nil {
		return nil, err
	}
	if err = identityName(parsed, name); err != nil {
		return nil, err
	}
	return &MutationResult{Fields: parsedFields(parsed, name), AgentsDir: directory, Target: path}, nil
}
func (s *Service) CreateFile(ctx context.Context, fields protocol.JSONObject, mutation MutationContext) (MutationResult, error) {
	name := fields.String("name")
	if err := ValidateName(name); err != nil {
		return MutationResult{}, err
	}
	for _, field := range []string{"description", "systemPrompt"} {
		if _, err := required(fields.Get(field), field); err != nil {
			return MutationResult{}, err
		}
	}
	directory, err := s.directory(mutation, true)
	if err != nil {
		return MutationResult{}, err
	}
	path := filepath.Join(directory, name+".md")
	document := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	if err = yamlSet(document, "name", quote(name)); err != nil {
		return MutationResult{}, err
	}
	if err = applyFields(document, fields); err != nil {
		return MutationResult{}, err
	}
	raw, err := render(document, "\n"+trim(fields.String("systemPrompt"))+"\n")
	if err != nil {
		return MutationResult{}, err
	}
	unlock := lockTarget(path)
	defer unlock()
	info, err := os.Lstat(directory)
	if err != nil {
		return MutationResult{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return MutationResult{}, unsafe("Agent directory must be a real directory")
	}
	real, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return MutationResult{}, err
	}
	if real != directory {
		return MutationResult{}, unsafe("Agent directory changed before mutation")
	}
	if err = atomicCreate(path, raw); err != nil {
		return MutationResult{}, err
	}
	return MutationResult{fields, directory, path}, nil
}
func (s *Service) UpdateFile(ctx context.Context, name string, fields protocol.JSONObject, mutation MutationContext) (MutationResult, error) {
	if err := ValidateName(name); err != nil {
		return MutationResult{}, err
	}
	if value := fields.Get("name"); value != nil && fields.String("name") != name {
		return MutationResult{}, bad("Agent name cannot be changed")
	}
	_, initial, err := s.resolveTarget(ctx, name, mutation)
	if err != nil {
		return MutationResult{}, err
	}
	unlock := lockTarget(initial)
	defer unlock()
	directory, path, err := s.resolveTarget(ctx, name, mutation)
	if err != nil {
		return MutationResult{}, err
	}
	if path != initial {
		return MutationResult{}, fail(409, "CONFLICT", "Agent target changed before the update started")
	}
	parsed, err := readFile(path)
	if err != nil {
		return MutationResult{}, err
	}
	if parsed == nil {
		return MutationResult{}, fail(404, "NOT_FOUND", "Agent not found in "+mutation.Scope+" scope: "+name)
	}
	if err = identityName(parsed, name); err != nil {
		return MutationResult{}, err
	}
	if err = yamlSet(parsed.document, "name", quote(name)); err != nil {
		return MutationResult{}, err
	}
	if err = applyFields(parsed.document, fields); err != nil {
		return MutationResult{}, err
	}
	body := parsed.body
	if fields.Get("systemPrompt") != nil {
		body = "\n" + trim(fields.String("systemPrompt")) + "\n"
	}
	raw, err := render(parsed.document, body)
	if err != nil {
		return MutationResult{}, err
	}
	if err = atomicReplace(path, raw, parsed.identity); err != nil {
		return MutationResult{}, err
	}
	updated, err := readFile(path)
	if err != nil {
		return MutationResult{}, err
	}
	if updated == nil {
		return MutationResult{}, fail(500, "INTERNAL_ERROR", "Failed to read updated agent: "+name)
	}
	return MutationResult{parsedFields(updated, name), directory, path}, nil
}
func (s *Service) DeleteFile(ctx context.Context, name string, mutation MutationContext) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	_, initial, err := s.resolveTarget(ctx, name, mutation)
	if err != nil {
		return err
	}
	unlock := lockTarget(initial)
	defer unlock()
	_, path, err := s.resolveTarget(ctx, name, mutation)
	if err != nil {
		return err
	}
	if path != initial {
		return fail(409, "CONFLICT", "Agent target changed before deletion started")
	}
	parsed, err := readFile(path)
	if err != nil {
		return err
	}
	if parsed == nil {
		return fail(404, "NOT_FOUND", "Agent not found in "+mutation.Scope+" scope: "+name)
	}
	if err = identityName(parsed, name); err != nil {
		return err
	}
	if err = assertFileIdentity(path, parsed.identity); err != nil {
		return err
	}
	tombstone, err := temporaryPath(path, ".deleted")
	if err != nil {
		return err
	}
	if err = os.Rename(path, tombstone); err != nil {
		return err
	}
	return os.Remove(tombstone)
}
