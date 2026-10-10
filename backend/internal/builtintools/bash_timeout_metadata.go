package builtintools

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionrewind"
)

func nativeBashEnvTimeout(env map[string]string, key string, fallback float64) float64 {
	value := protocol.TrimJavaScriptString(env[key])
	if strings.HasPrefix(value, "+") {
		value = value[1:]
	} else if strings.HasPrefix(value, "-") {
		return fallback
	}
	end := 0
	for end < len(value) && value[end] >= '0' && value[end] <= '9' {
		end++
	}
	if end == 0 {
		return fallback
	}
	number, _ := strconv.ParseFloat(value[:end], 64)
	if number > 0 {
		return number
	}
	return fallback
}
func nativeBashDefaultTimeout(env map[string]string) float64 {
	return nativeBashEnvTimeout(env, "BASH_DEFAULT_TIMEOUT_MS", 120000)
}
func nativeBashMaxTimeout(env map[string]string) float64 {
	return math.Max(nativeBashEnvTimeout(env, "BASH_MAX_TIMEOUT_MS", 600000), nativeBashDefaultTimeout(env))
}
func nativeBashTimeout(in protocol.JSONObject, env map[string]string) float64 {
	var value float64
	if json.Unmarshal(in.Get("timeout"), &value) == nil && value != 0 {
		return value
	}
	return nativeBashDefaultTimeout(env)
}
func nativeBashTimerDuration(milliseconds float64) time.Duration {
	// Bun/Node native setTimeout truncates fractions and maps out-of-range or
	// negative delays to one millisecond; the documented max is not a clamp.
	if milliseconds < 1 || milliseconds > 2147483647 || math.IsNaN(milliseconds) {
		return time.Millisecond
	}
	return time.Duration(math.Floor(milliseconds)) * time.Millisecond
}
func nativeBashSilent(command, platform string) bool {
	classifier, e := sessionrewind.NewRecordedCommandClassifier(platform, "")
	if e != nil {
		return false
	}
	parts, e := classifier.SplitWithOperators(command)
	if e != nil || len(parts) == 0 {
		return false
	}
	silent := map[string]bool{"mv": true, "cp": true, "rm": true, "mkdir": true, "rmdir": true, "chmod": true, "chown": true, "chgrp": true, "touch": true, "ln": true, "cd": true, "export": true, "unset": true, "wait": true}
	neutral := map[string]bool{"echo": true, "printf": true, "true": true, "false": true, ":": true}
	found, redirect := false, false
	operator := ""
	for _, part := range parts {
		if redirect {
			redirect = false
			continue
		}
		switch part {
		case ">", ">>", ">&":
			redirect = true
			continue
		case "||", "&&", "|", ";":
			operator = part
			continue
		}
		trimmed := protocol.TrimJavaScriptString(part)
		end := strings.IndexFunc(trimmed, protocol.IsJavaScriptWhitespace)
		if end >= 0 {
			trimmed = trimmed[:end]
		}
		if trimmed == "" {
			continue
		}
		if operator == "||" && neutral[trimmed] {
			continue
		}
		found = true
		if !silent[trimmed] {
			return false
		}
	}
	return found
}
