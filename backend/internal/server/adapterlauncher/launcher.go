// Package adapterlauncher owns the original desktop adapters launcher boundary.
// Platform runtimes still missing in Go fail explicitly after the actual gate.
package adapterlauncher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type Options struct {
	Environment      map[string]string
	HomeDir, WorkDir string
	Stderr           io.Writer
	Stdout           io.Writer
	StartFeishu      func(context.Context, Options) (Process, error)
	StartWhatsApp    func(context.Context, Options) (Process, error)
	StartTelegram    func(context.Context, Options) (Process, error)
	StartSlack       func(context.Context, Options) (Process, error)
	StartWechat      func(context.Context, Options) (Process, error)
	StartWecom       func(context.Context, Options) (Process, error)
	StartQQ          func(context.Context, Options) (Process, error)
	StartDingTalk    func(context.Context, Options) (Process, error)
}
type Process interface {
	Wait() error
	Close()
}
type CapabilityError struct{ Adapter string }

func (err *CapabilityError) Error() string {
	return "Go IM adapter runtime is not migrated: " + err.Adapter
}

type Invocation struct {
	AppRoot  *string
	Enabled  map[string]bool
	Warnings []string
}
type definition struct{ flag, label, missing, key, first, second, firstEnv, secondEnv string }

var adapters = []definition{
	{"--feishu", "Feishu", "FEISHU_APP_ID / FEISHU_APP_SECRET missing", "feishu", "appId", "appSecret", "FEISHU_APP_ID", "FEISHU_APP_SECRET"},
	{"--telegram", "Telegram", "TELEGRAM_BOT_TOKEN missing", "telegram", "botToken", "", "TELEGRAM_BOT_TOKEN", ""},
	{"--wechat", "WeChat", "no QR-bound WeChat account found", "wechat", "accountId", "botToken", "WECHAT_ACCOUNT_ID", "WECHAT_BOT_TOKEN"},
	{"--dingtalk", "DingTalk", "DINGTALK_CLIENT_ID / DINGTALK_CLIENT_SECRET missing", "dingtalk", "clientId", "clientSecret", "DINGTALK_CLIENT_ID", "DINGTALK_CLIENT_SECRET"},
	{"--whatsapp", "WhatsApp", "no QR-linked WhatsApp account found", "whatsapp", "", "", "", ""},
	{"--wecom", "WeCom", "WECOM_BOT_ID / WECOM_BOT_SECRET missing", "wecom", "botId", "secret", "WECOM_BOT_ID", "WECOM_BOT_SECRET"},
	{"--qq", "QQ", "QQ_APP_ID / QQ_APP_SECRET missing", "qq", "appId", "appSecret", "QQ_APP_ID", "QQ_APP_SECRET"},
	{"--slack", "Slack", "SLACK_BOT_TOKEN / SLACK_APP_TOKEN missing", "slack", "botToken", "appToken", "SLACK_BOT_TOKEN", "SLACK_APP_TOKEN"},
}

func flags() []string {
	result := []string{}
	for _, adapter := range adapters {
		result = append(result, adapter.flag)
	}
	return result
}

// Parse reproduces the launcher-only flag consumption, before CLI parsing.
func Parse(args []string, env map[string]string) Invocation {
	result := Invocation{Enabled: map[string]bool{}, Warnings: []string{}}
	if value, exists := env["CLAUDE_APP_ROOT"]; exists {
		result.AppRoot = &value
	}
	known := map[string]bool{}
	for _, adapter := range adapters {
		known[adapter.flag] = true
	}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--app-root" {
			result.AppRoot = nil
			if index+1 < len(args) {
				value := args[index+1]
				result.AppRoot = &value
			}
			index++
			continue
		}
		if known[arg] {
			result.Enabled[arg] = true
			continue
		}
		result.Warnings = append(result.Warnings, fmt.Sprintf("claude-sidecar adapters: ignoring unknown arg \"%s\"", arg))
	}
	return result
}

func jsonTruthy(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) || bytes.Equal(raw, []byte("false")) {
		return false
	}
	if raw[0] == '"' {
		value, err := protocol.DecodeJavaScriptJSONString(raw)
		return err == nil && len(value) != 0
	}
	if raw[0] == '-' || raw[0] >= '0' && raw[0] <= '9' {
		value, err := strconv.ParseFloat(string(raw), 64)
		return (err == nil || math.IsInf(value, 0)) && value != 0 && !math.IsNaN(value)
	}
	return true
}
func child(raw json.RawMessage) map[string]json.RawMessage {
	var value map[string]json.RawMessage
	_ = json.Unmarshal(raw, &value)
	return value
}
func credential(env map[string]string, section map[string]json.RawMessage, envKey, key string) bool {
	return env[envKey] != "" || jsonTruthy(section[key])
}
func jsTrim(value string) string {
	return strings.TrimFunc(value, func(r rune) bool { return r == '\uFEFF' || unicode.IsSpace(r) && r != '\u0085' })
}
func whatsappAuth(options Options, config map[string]json.RawMessage, configDir string) (bool, error) {
	value := options.Environment["WHATSAPP_AUTH_DIR"]
	if value == "" && jsonTruthy(config["authDir"]) {
		if json.Unmarshal(config["authDir"], &value) != nil {
			return false, errors.New("WhatsApp authDir must support string trimming")
		}
	}
	if value == "" {
		value = filepath.Join(configDir, "whatsapp-auth", "default")
	}
	value = jsTrim(value)
	if value == "" {
		value = filepath.Join(configDir, "whatsapp-auth", "default")
	}
	if value == "~" {
		value = options.HomeDir
	} else if strings.HasPrefix(value, "~/") {
		value = filepath.Join(options.HomeDir, value[2:])
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(options.WorkDir, value)
	}
	_, err := os.Stat(filepath.Join(value, "creds.json"))
	return err == nil, nil
}

func Run(ctx context.Context, args []string, options Options) (int, error) {
	if !filepath.IsAbs(options.HomeDir) || !filepath.IsAbs(options.WorkDir) || options.Stderr == nil {
		return 1, errors.New("adapters launcher requires explicit native home/cwd/stderr")
	}
	if err := ctx.Err(); err != nil {
		return 1, err
	}
	invocation := Parse(args, options.Environment)
	for _, warning := range invocation.Warnings {
		fmt.Fprintln(options.Stderr, warning)
	}
	if len(invocation.Enabled) == 0 {
		fmt.Fprintln(options.Stderr, "claude-sidecar adapters: must enable at least one of "+strings.Join(flags(), " / "))
		return 2, nil
	}
	env := map[string]string{}
	for key, value := range options.Environment {
		env[key] = value
	}
	options.Environment = env
	if invocation.AppRoot != nil && *invocation.AppRoot != "" {
		env["CLAUDE_APP_ROOT"] = *invocation.AppRoot
	}
	if env["CALLER_DIR"] == "" {
		env["CALLER_DIR"] = options.WorkDir
	}
	caller := env["CALLER_DIR"]
	if !filepath.IsAbs(caller) {
		caller = filepath.Join(options.WorkDir, caller)
	}
	info, callerErr := os.Stat(caller)
	if callerErr != nil {
		return 1, callerErr
	}
	if !info.IsDir() {
		return 1, errors.New("CALLER_DIR is not a directory")
	}
	options.WorkDir = filepath.Clean(caller)
	configDir := env["CLAUDE_CONFIG_DIR"]
	if configDir == "" {
		configDir = filepath.Join(options.HomeDir, ".claude")
	}
	if !filepath.IsAbs(configDir) {
		configDir = filepath.Join(options.WorkDir, configDir)
	}
	path := filepath.Join(configDir, "adapters.json")
	file := map[string]json.RawMessage{}
	raw, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(options.Stderr, "[Config] Failed to parse %s, using defaults\n", path)
		}
	} else {
		raw = protocol.DecodeJavaScriptUTF8(raw)
		if !json.Valid(raw) {
			fmt.Fprintf(options.Stderr, "[Config] Failed to parse %s, using defaults\n", path)
		} else {
			if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				return 1, errors.New("adapters.json null does not support property access")
			}
			_ = json.Unmarshal(raw, &file)
			if file == nil {
				file = map[string]json.RawMessage{}
			}
		}
	}
	processes := []Process{}
	var waits sync.WaitGroup
	done := make(chan error, len(adapters))
	defer func() {
		for _, process := range processes {
			process.Close()
		}
		waits.Wait()
	}()
	for _, adapter := range adapters {
		if !invocation.Enabled[adapter.flag] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return 1, err
		}
		section := child(file[adapter.key])
		configured := false
		if adapter.key == "whatsapp" {
			configured, err = whatsappAuth(options, section, configDir)
			if err != nil {
				return 1, err
			}
		} else {
			configured = credential(env, section, adapter.firstEnv, adapter.first) && (adapter.second == "" || credential(env, section, adapter.secondEnv, adapter.second))
		}
		if !configured {
			fmt.Fprintf(options.Stderr, "[claude-sidecar] %s requested but %s in env or ~/.claude/adapters.json — skipping\n", adapter.flag, adapter.missing)
			continue
		}
		var start func(context.Context, Options) (Process, error)
		if adapter.key == "feishu" {
			start = options.StartFeishu
		}
		if adapter.key == "whatsapp" {
			start = options.StartWhatsApp
		}
		if adapter.key == "telegram" {
			start = options.StartTelegram
		}
		if adapter.key == "slack" {
			start = options.StartSlack
		}
		if adapter.key == "wechat" {
			start = options.StartWechat
		}
		if adapter.key == "wecom" {
			start = options.StartWecom
		}
		if adapter.key == "qq" {
			start = options.StartQQ
		}
		if adapter.key == "dingtalk" {
			start = options.StartDingTalk
		}
		if start != nil {
			output := options.Stdout
			if output == nil {
				output = options.Stderr
			}
			fmt.Fprintf(output, "[claude-sidecar] starting %s adapter\n", adapter.label)
			process, err := start(ctx, options)
			if err != nil {
				return 1, err
			}
			processes = append(processes, process)
			waits.Add(1)
			go func() { defer waits.Done(); done <- process.Wait() }()
			continue
		}
		return 1, &CapabilityError{Adapter: adapter.label}
	}
	if len(processes) > 0 {
		select {
		case <-ctx.Done():
			return 0, nil
		case err := <-done:
			if err != nil {
				return 1, err
			}
			return 0, nil
		}
	}
	fmt.Fprintln(options.Stderr, "[claude-sidecar] no adapter could be started — check credentials in env or ~/.claude/adapters.json")
	return 1, nil
}
