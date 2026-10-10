package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/javascriptparse"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"path/filepath"
	"strings"
)

func fatal(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
func main() {
	home := os.Getenv("HOME")
	canonical, err := filepath.EvalSymlinks(home)
	if err != nil || canonical != home || !filepath.IsAbs(home) {
		fatal(fmt.Errorf("canonical private HOME required"))
	}
	cwd, err := os.Getwd()
	if err != nil || cwd != home || os.Getenv("USERPROFILE") != home || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(home, ".claude") || os.Getenv("CC_HAHA_ACORN_LANGUAGE_FIXTURE") != "1" || os.Getenv("BUN_OPTIONS") != "--no-env-file" {
		fatal(fmt.Errorf("fixture private environment required"))
	}
	root, err := filepath.EvalSymlinks(os.Getenv("CC_HAHA_FIXTURE_TEMP_ROOT"))
	if err != nil || filepath.Dir(home) != root || !strings.HasPrefix(filepath.Base(home), "cc-haha-acorn-") {
		fatal(fmt.Errorf("trusted disposable allocation required"))
	}
	for _, name := range []string{".claude", "input.json", "result.json"} {
		stat, err := os.Lstat(filepath.Join(home, name))
		if os.IsNotExist(err) && name == "result.json" {
			continue
		}
		if err != nil || stat.Mode()&os.ModeSymlink != 0 {
			fatal(fmt.Errorf("private nonsymlink known IO required"))
		}
	}
	raw, err := os.ReadFile(filepath.Join(home, "input.json"))
	if err != nil {
		fatal(err)
	}
	var inputs []json.RawMessage
	if err = json.Unmarshal(raw, &inputs); err != nil || len(inputs) > 100 {
		fatal(fmt.Errorf("bounded original array required"))
	}
	type row struct {
		ID     string                 `json:"id"`
		Group  string                 `json:"group"`
		Result javascriptparse.Result `json:"result"`
	}
	rows := []row{}
	owner := cooperative.New()
	defer owner.Close()
	err = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		parser, err := javascriptparse.New(scope, owner)
		if err != nil {
			return err
		}
		for _, input := range inputs {
			var object protocol.JSONObject
			if err := json.Unmarshal(input, &object); err != nil || !object.IsObject() {
				return fmt.Errorf("original object required: %v", err)
			}
			for _, property := range object.OwnProperties() {
				var key string
				if err := json.Unmarshal(property.NameJSON, &key); err != nil {
					return err
				}
				if key != "id" && key != "group" && key != "origin" && key != "script" {
					return fmt.Errorf("unknown original input field")
				}
			}
			var id, group, origin string
			for key, target := range map[string]*string{"id": &id, "group": &group, "origin": &origin} {
				if err = json.Unmarshal(object.Get(key), target); err != nil {
					return err
				}
			}
			units, err := protocol.DecodeJavaScriptJSONString(object.Get("script"))
			if err != nil {
				return err
			}
			if len(units) > 700000 {
				return fmt.Errorf("bounded script required")
			}
			result, err := parser.Parse(scope, units)
			var cap *javascriptparse.CapabilityError
			if err != nil && !errors.As(err, &cap) {
				return err
			}
			rows = append(rows, row{id, group, result})
		}
		return nil
	})
	if err != nil {
		fatal(err)
	}
	value := struct {
		Result       []row               `json:"result"`
		Body         bool                `json:"bodyExecuted"`
		Capabilities map[string][]string `json:"capabilities"`
	}{rows, false, map[string][]string{"blocked": {}, "secureReads": {}, "absentReads": {}}}
	encoded, err := json.Marshal(value)
	if err != nil {
		fatal(err)
	}
	if err = os.WriteFile(filepath.Join(home, "result.json"), encoded, 0600); err != nil {
		fatal(err)
	}
	sum := sha256.Sum256(encoded)
	fmt.Printf("{\"bytes\":%d,\"sha256\":%q}\n", len(encoded), hex.EncodeToString(sum[:]))
}
