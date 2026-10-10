package tlsconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type operation struct {
	Op, Name, Hex, Action, Options, Flag string
	Index                                int
	Value                                json.RawMessage
}
type originalCase struct {
	Name       string
	Operations []operation
}
type readObservation struct {
	Path string `json:"path"`
	Code string `json:"code"`
}
type certificateObservation struct {
	Identity int      `json:"identity"`
	Count    int      `json:"count"`
	SHA256   string   `json:"sha256"`
	Tail     []string `json:"tail"`
	First    string   `json:"first"`
}
type stepObservation struct {
	Certificates *certificateObservation `json:"certificates"`
	Reads        []readObservation       `json:"reads"`
}
type observation struct {
	Name  string `json:"name"`
	Steps []any  `json:"steps"`
}

func validateOriginalCase(row originalCase) error {
	name := regexp.MustCompile(`^[a-z]+$`)
	for _, op := range row.Operations {
		switch op.Op {
		case "write", "mkdir", "remove":
			if !name.MatchString(op.Name) {
				return fmt.Errorf("private original filename required")
			}
		case "env":
			var values map[string]string
			if err := json.Unmarshal(op.Value, &values); err != nil || values == nil {
				return fmt.Errorf("private original env required")
			}
			for key, value := range values {
				if key != "NODE_EXTRA_CA_CERTS" && key != "NODE_OPTIONS" {
					return fmt.Errorf("original env key is unported")
				}
				if key == "NODE_EXTRA_CA_CERTS" && value != "" && value != " " && (!strings.HasPrefix(value, "file:") || !name.MatchString(strings.TrimPrefix(value, "file:"))) {
					return fmt.Errorf("private original env/path required")
				}
			}
		case "get", "clear", "option":
		case "mutate":
			if op.Action != "set" && op.Action != "push" && op.Action != "pop" {
				return fmt.Errorf("original mutation is unported")
			}
		default:
			return fmt.Errorf("original operation is unported")
		}
	}
	return nil
}

func errorCode(err error) string {
	if err == nil {
		return ""
	}
	if os.IsNotExist(err) {
		return "ENOENT"
	}
	if strings.Contains(err.Error(), "is a directory") {
		return "EISDIR"
	}
	return "unported:" + err.Error()
}
func observe(row originalCase, home string) (observation, error) {
	result := observation{Name: row.Name, Steps: []any{}}
	if err := validateOriginalCase(row); err != nil {
		return result, err
	}
	env := map[string]string{}
	reads := []readObservation{}
	state, _ := NewState(func(path string) ([]byte, error) {
		if !filepath.IsAbs(path) {
			path = filepath.Join(home, path)
		}
		raw, err := os.ReadFile(path)
		relative, _ := filepath.Rel(home, path)
		reads = append(reads, readObservation{filepath.ToSlash(relative), errorCode(err)})
		return raw, err
	})
	identities := map[*CertificateArray]int{}
	var current *CertificateArray
	folder := filepath.Join(home, "certs")
	if err := os.MkdirAll(folder, 0700); err != nil {
		return result, err
	}
	defer os.RemoveAll(folder)
	path := func(name string) string { return filepath.Join(folder, name) }
	for _, op := range row.Operations {
		switch op.Op {
		case "write":
			raw, err := hex.DecodeString(op.Hex)
			if err != nil {
				return result, err
			}
			if err = os.WriteFile(path(op.Name), raw, 0600); err != nil {
				return result, err
			}
		case "mkdir":
			if err := os.Mkdir(path(op.Name), 0700); err != nil {
				return result, err
			}
		case "remove":
			if err := os.RemoveAll(path(op.Name)); err != nil {
				return result, err
			}
		case "env":
			if err := json.Unmarshal(op.Value, &env); err != nil {
				return result, err
			}
			for key, value := range env {
				if key == "NODE_EXTRA_CA_CERTS" && strings.HasPrefix(value, "file:") {
					env[key] = path(strings.TrimPrefix(value, "file:"))
				}
			}
		case "clear":
			state.Clear()
		case "mutate":
			if current == nil {
				return result, fmt.Errorf("mutation requires array")
			}
			switch op.Action {
			case "push":
				current.Items = append(current.Items, op.Value)
			case "set":
				current.Items[op.Index] = op.Value
			case "pop":
				current.Items = current.Items[:len(current.Items)-1]
			default:
				return result, fmt.Errorf("unknown mutation")
			}
		case "option":
			result.Steps = append(result.Steps, map[string]bool{"option": HasNodeOption(op.Options, op.Flag)})
		case "get":
			var err error
			current, err = state.Certificates(env)
			if err != nil {
				return result, err
			}
			step := stepObservation{Reads: append([]readObservation{}, reads...)}
			if current != nil {
				if _, ok := identities[current]; !ok {
					identities[current] = len(identities) + 1
				}
				raw, err := json.Marshal(current.Items)
				if err != nil {
					return result, err
				}
				raw = protocol.StringifyValidatedJavaScriptJSON(raw)
				sha := sha256.Sum256(raw)
				step.Certificates = &certificateObservation{Identity: identities[current], Count: len(current.Items), SHA256: hex.EncodeToString(sha[:]), Tail: []string{}, First: string(protocol.StringifyValidatedJavaScriptJSON(current.Items[0]))}
				for _, item := range current.Items[min(145, len(current.Items)):] {
					step.Certificates.Tail = append(step.Certificates.Tail, string(protocol.StringifyValidatedJavaScriptJSON(item)))
				}
			}
			result.Steps = append(result.Steps, step)
		default:
			return result, fmt.Errorf("unknown operation %q", op.Op)
		}
	}
	return result, nil
}
func TestTLSConfigFixture(t *testing.T) {
	if os.Getenv("CC_HAHA_TLS_CONFIG_FIXTURE") != "1" {
		t.Skip("actual Source differential child")
	}
	var input struct {
		Home string
		Rows []originalCase
	}
	decoder := json.NewDecoder(os.Stdin)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		t.Fatal(err)
	}
	home := input.Home
	cwd, _ := os.Getwd()
	canonical, _ := filepath.EvalSymlinks(home)
	canonicalCwd, _ := filepath.EvalSymlinks(cwd)
	info, err := os.Lstat(home)
	allocation, rootErr := filepath.EvalSymlinks(os.Getenv("CC_HAHA_FIXTURE_TEMP_ROOT"))
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || home != os.Getenv("HOME") || canonical != canonicalCwd || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(home, ".claude") || rootErr != nil || !strings.HasPrefix(canonical, allocation+string(os.PathSeparator)) {
		t.Fatal("TLS config requires disposable HOME/config")
	}
	results := []observation{}
	for _, row := range input.Rows {
		result, err := observe(row, home)
		if err != nil {
			t.Fatal(err)
		}
		results = append(results, result)
	}
	raw, err := json.Marshal(map[string]any{"observations": results})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("TLS_CONFIG_RESULT:%s\n", raw)
}
