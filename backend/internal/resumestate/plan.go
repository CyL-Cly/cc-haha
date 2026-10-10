package resumestate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionresume"
)

func fmtPlanRecovery(source string, length int) string {
	return fmt.Sprintf("Plan recovered from %s, %d chars", source, length)
}
func (service *Service) plansDir() (string, error) {
	service.planMu.Lock()
	defer service.planMu.Unlock()
	service.mu.Lock()
	path := service.plansDirectory
	service.mu.Unlock()
	if path != "" {
		return path, nil
	}
	settings, _, err := service.settings()
	if err != nil {
		return "", err
	}
	path = filepath.Join(service.options.ConfigDir, "plans")
	if configured := settings.Get("plansDirectory"); truthy(configured) {
		resolved := text(configured)
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(service.options.CWD, resolved)
		}
		resolved = filepath.Clean(resolved)
		if resolved != service.options.CWD && !strings.HasPrefix(resolved, service.options.CWD+string(filepath.Separator)) {
			service.options.OnError(failure("plansDirectory must be within project root: %s", text(configured)))
		} else {
			path = resolved
		}
	}
	if err := service.options.Files.MkdirAll(path); err != nil {
		service.options.OnError(err)
	}
	service.mu.Lock()
	if service.plansDirectory == "" {
		service.plansDirectory = path
	}
	path = service.plansDirectory
	service.mu.Unlock()
	return path, nil
}
func (service *Service) copyPlan(log protocol.JSONObject, sid json.RawMessage) (bool, error) {
	messages, _ := array(log.Get("messages"))
	var slug json.RawMessage
	for _, message := range messages {
		candidate, err := property(message, "slug", "m.slug")
		if err != nil {
			return false, err
		}
		if truthy(candidate) {
			slug = candidate
			break
		}
	}
	if !truthy(slug) {
		return false, nil
	}
	service.setSlug(sid, slug)
	directory, err := service.plansDir()
	if err != nil {
		return false, err
	}
	coerced, err := jsString(slug)
	if err != nil {
		return false, err
	}
	path := filepath.Join(directory, text(coerced)+".md")
	if _, err = service.options.Files.ReadFile(path); err == nil {
		return true, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		service.options.OnError(err)
		return false, nil
	}
	if service.options.EnvironmentKind != "byoc" && service.options.EnvironmentKind != "anthropic_cloud" {
		return false, nil
	}
	service.diagnostic("debug", "Plan file missing during resume: "+path+". Attempting recovery.", nil)
	var recovered json.RawMessage
	for index := len(messages) - 1; index >= 0; index-- {
		message := object(messages[index])
		if message.String("type") != "system" || message.String("subtype") != "file_snapshot" || message.Get("snapshotFiles") == nil {
			continue
		}
		files, valid := array(message.Get("snapshotFiles"))
		if !valid {
			return false, &sessionresume.Failure{Name: "TypeError", Message: "msg.snapshotFiles.find is not a function. (In 'msg.snapshotFiles.find((f) => f.key === key)', 'msg.snapshotFiles.find' is undefined)"}
		}
		for _, item := range files {
			name, err := property(item, "key", "f.key")
			if err != nil {
				return false, err
			}
			if scalarEqual(name, quote("plan")) {
				content, err := property(item, "content", "snapshotPlan.content.length")
				if err != nil {
					return false, err
				}
				if _, err := property(content, "length", "snapshotPlan.content.length"); err != nil {
					return false, err
				}
				if value, valid := units(content); valid && len(value) > 0 {
					recovered = content
				}
				break
			}
		}
		break
	}
	if recovered != nil {
		value, _ := units(recovered)
		service.diagnostic("debug", fmtPlanRecovery("file snapshot", len(value)), []byte(`{"level":"info"}`))
	} else {
		for index := len(messages) - 1; index >= 0 && recovered == nil; index-- {
			message := object(messages[index])
			switch message.String("type") {
			case "assistant":
				api, err := property(messages[index], "message", "msg.message")
				if err != nil {
					return false, err
				}
				if api == nil || isNull(api) {
					return false, &sessionresume.Failure{Name: "TypeError", Message: "Cannot destructure property 'content' from null or undefined value"}
				}
				blocks, _ := array(object(api).Get("content"))
				for _, block := range blocks {
					rawType, err := property(block, "type", "block.type")
					if err != nil {
						return false, err
					}
					if text(rawType) == "tool_use" && object(block).String("name") == "ExitPlanMode" {
						candidate := object(object(block).Get("input")).Get("plan")
						if value, valid := units(candidate); valid && len(value) > 0 {
							recovered = candidate
							break
						}
					}
				}
			case "user":
				candidate := message.Get("planContent")
				if value, valid := units(candidate); valid && len(value) > 0 {
					recovered = candidate
				}
			case "attachment":
				payload := object(message.Get("attachment"))
				if payload.String("type") == "plan_file_reference" {
					candidate := payload.Get("planContent")
					if value, valid := units(candidate); valid && len(value) > 0 {
						recovered = candidate
					}
				}
			}
		}
		if recovered != nil {
			value, _ := units(recovered)
			service.diagnostic("debug", fmtPlanRecovery("message history", len(value)), []byte(`{"level":"info"}`))
		}
	}
	if recovered != nil {
		value, _ := units(recovered)
		if err = service.options.Files.WriteFile(path, []byte(string(utf16.Decode(value)))); err != nil {
			service.options.OnError(err)
			return false, nil
		}
		return true, nil
	}
	service.diagnostic("debug", "Plan file recovery failed: no file snapshot or plan content found in message history", nil)
	return false, nil
}
