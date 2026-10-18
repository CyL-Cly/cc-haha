package sdkhost

import (
	"bytes"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"strings"
	"unicode/utf16"
)

func slashParts(content json.RawMessage) (name, args string, slash bool, err error) {
	units, e := protocol.DecodeJavaScriptJSONString(content)
	if e != nil {
		var blocks []protocol.JSONObject
		if e := json.Unmarshal(content, &blocks); e != nil || blocks == nil {
			return "", "", false, cap("SDK native text input shape")
		}
		if len(blocks) == 0 {
			return "", "", false, nil
		}
		if blocks[len(blocks)-1].String("type") != "text" {
			return "", "", false, nil
		}
		units, e = protocol.DecodeJavaScriptJSONString(blocks[len(blocks)-1].Get("text"))
		if e != nil {
			return "", "", false, e
		}
	}
	// Source processUserInput tests startsWith('/') before the parser trims.
	if len(units) == 0 || units[0] != '/' {
		return "", "", false, nil
	}
	for i, u := range units {
		if u >= 0xD800 && u <= 0xDFFF && (i == 0 || units[i-1] < 0xD800 || units[i-1] > 0xDBFF) && (i+1 == len(units) || units[i+1] < 0xDC00 || units[i+1] > 0xDFFF) {
			return "", "", true, cap("slash UTF-16 surrogate domain")
		}
	}
	rawInput := strings.TrimSpace(string(utf16.Decode(units)))
	parts := strings.Split(rawInput[1:], " ")
	name = parts[0]
	args = strings.Join(parts[1:], " ")
	if len(parts) > 1 && parts[1] == "(MCP)" {
		name += " (MCP)"
		args = strings.Join(parts[2:], " ")
	}
	return name, args, true, nil
}
func (h *Host) validatePrintUserInput(content json.RawMessage) error {
	if h.options.Print != nil && h.options.Print.Registry != nil && h.options.Print.Registry.SlashCommandsDisabled() {
		return nil
	}
	if h.terminalBash != nil && bytes.Equal(content, h.terminalBash.content) {
		_, err := protocol.DecodeJavaScriptJSONString(content)
		return err
	}
	name, _, slash, e := slashParts(content)
	if e != nil || !slash {
		return e
	}
	if name == "compact" && h.options.Print.NativeProcess {
		return nil
	}
	if name == "release-notes" || name == "context" || name == "heapdump" || (name == "effort" || name == "vim" || name == "stickers" || name == "keybindings" && h.options.Print.NativeKeybindingsEnabled != nil && h.options.Print.NativeKeybindingsEnabled()) && h.options.Print.NativeInteractive {
		if h.options.Print.NativeLocalCommand == nil {
			return cap("native local command /" + name)
		}
		return nil
	}
	if name == "plan" && h.options.Print.NativeInteractive {
		if h.options.Print.NativePlanCommand == nil {
			return cap("actual /plan local JSX executor")
		}
		return nil
	}
	if (name == "rewind" || name == "checkpoint") && h.options.Print.NativeInteractive {
		if h.options.Print.NativeRewindCommand == nil {
			return cap("actual /rewind local command executor")
		}
		return nil
	}
	if name == "terminal-setup" && h.options.Print.NativeInteractive {
		if h.options.Print.NativeTerminalSetupCommand == nil {
			return cap("actual /terminal-setup local JSX executor")
		}
		return nil
	}
	if name == "goal" {
		if h.options.Print.NativeGoalCommand == nil {
			return cap("actual /goal local JSX executor")
		}
		return nil
	}
	if name != "cost" {
		if h.options.Print.ProcessSkillPrompt != nil {
			// Resolve the current native inventory during the owned input stage.
			// Validation must not execute the prompt or record skill usage.
			return nil
		}
		if h.options.Print.SkillInvocation != nil {
			if _, exists := h.options.Print.Registry.Prompt(name); exists {
				return nil
			}
		}
		return cap("unmigrated processUserInput slash handler /" + name)
	}
	return h.options.Print.Registry.requireCostCommand()
}
func (h *Host) processPrintUserInput(scope *cooperative.Scope, content, uuid json.RawMessage) (*sessioncontroller.ProcessedInput, error) {
	return h.processQueuedPrintInput(scope, content, uuid, false, false, nil)
}

func (h *Host) processQueuedPrintInputBaseNormalized(scope *cooperative.Scope, content, uuid json.RawMessage, skipSlash, meta bool, origin json.RawMessage, skipAttachments bool) (*sessioncontroller.ProcessedInput, error) {
	if restarted, e := h.consumeAgentRestartPrompt(scope, content); e != nil || restarted != nil {
		return restarted, e
	}
	name, args, slash, e := slashParts(content)
	if skipSlash {
		name, args, slash, e = "", "", false, nil
	}
	if e != nil || !slash {
		if e == nil && h.options.Print.NativeProcess {
			_, permission, e := h.options.Controller.SDKSettingsOnOwner(scope)
			if e != nil {
				return nil, e
			}
			var metadata []json.RawMessage
			// Terminal media was already resized by its actual paste/attachment
			// producer. Normalizing it as a second structured SDK image duplicates
			// dimension metadata and can downsample the same image twice.
			_, terminalMedia := h.terminalInputImageMetadata()
			if inputContainsBase64Image(content) && !terminalMedia {
				if h.options.Print.NativeInputMedia == nil {
					return nil, cap("actual SDK input media normalization")
				}
				ctx := h.inputModelContext
				if ctx == nil {
					return nil, cap("actual SDK input media lifetime")
				}
				content, metadata, e = h.options.Print.NativeInputMedia(ctx, scope, content)
				if e != nil {
					return nil, e
				}
			}
			// processUserInputBase awaits Promise.all(imageContents.map(...))
			// even when an SDK string has no pasted image. The already-completed
			// normalized image batch still crosses that real Promise reaction.
			if e = scope.AwaitFulfilled(); e != nil {
				return nil, e
			}
			attachments, attachmentsHandled, e := h.nativeInitialInputAttachments(scope, content, skipAttachments)
			if e != nil {
				return nil, e
			}
			options := storage.Object{"content": content, "uuid": uuid, "permissionMode": permission.Get("mode")}
			if ctx := h.inputModelContext; ctx != nil {
				if pasted, ok := ctx.Value(inputImageMetadataKey{}).(inputImageMetadata); ok {
					if len(pasted.IDs) > 0 {
						options["imagePasteIds"] = raw(pasted.IDs)
					}
					metadata = append(metadata, pasted.Texts...)
				}
			}
			if meta {
				options["isMeta"] = raw(true)
			}
			if origin != nil {
				options["origin"] = origin
			}
			user := messages.OrderedUser(h.options.Factory.CreateUser(options))
			values := append([]protocol.JSONObject{user}, attachments...)
			if len(metadata) > 0 {
				var mediaValues []protocol.JSONObject
				mediaValues, e = h.appendInputMediaMetadata(scope, user, metadata)
				if e != nil {
					return nil, e
				}
				values = append(values, mediaValues[1:]...)
			}
			return &sessioncontroller.ProcessedInput{Messages: values, AttachmentsHandled: attachmentsHandled || len(metadata) > 0}, nil
		}
		return nil, e
	}
	if h.options.Print.Registry.SlashCommandsDisabled() {
		result, err := h.processNativePromptInput(scope, name, args, uuid)
		if result != nil && err == nil {
			return result, retainSlashPrecedingBlocks(result, content)
		}
		return result, err
	}
	if e = h.validatePrintUserInput(content); e != nil {
		return nil, e
	}
	if name == "compact" && h.options.Print.NativeProcess {
		return h.processCompactInput(scope, args)
	}
	if name == "release-notes" || name == "context" || name == "heapdump" || (name == "effort" || name == "vim" || name == "stickers" || name == "keybindings" && h.options.Print.NativeKeybindingsEnabled != nil && h.options.Print.NativeKeybindingsEnabled()) && h.options.Print.NativeInteractive {
		return h.processNativeLocalInput(scope, name, args)
	}
	if name == "plan" && h.options.Print.NativeInteractive {
		return h.processNativePlanInput(scope, args)
	}
	if (name == "rewind" || name == "checkpoint") && h.options.Print.NativeInteractive {
		return h.processNativeRewindInput(scope, args)
	}
	if name == "terminal-setup" && h.options.Print.NativeInteractive {
		return h.processNativeTerminalSetupInput(scope, args)
	}
	if name == "goal" {
		return h.processNativeGoalInput(scope, args)
	}
	if name != "cost" {
		result, err := h.processNativePromptInput(scope, name, args, uuid)
		if err != nil || result == nil {
			return result, err
		}
		return result, retainSlashPrecedingBlocks(result, content)
	}
	start := h.options.NowMillis()
	env := h.options.OriginalEnv
	if h.options.Print != nil && h.options.Print.NativeProcess {
		if _, e = nativeCostSubscriber(env); e != nil {
			return nil, e
		}
	} else if env["ANTHROPIC_API_KEY"] == "" || env["ANTHROPIC_AUTH_TOKEN"] != "" || env["CLAUDE_CODE_REMOTE"] != "" || env["USER_TYPE"] != "external" || env["CLAUDE_CODE_ENTRYPOINT"] != "sdk-cli" {
		return nil, cap("/cost external API-key subscriber admission")
	}
	tagged := "<command-name>/" + name + "</command-name>\n            <command-message>" + name + "</command-message>\n            <command-args>" + args + "</command-args>"
	user := messages.OrderedUser(h.options.Factory.CreateUser(storage.Object{"content": quote(tagged)}))
	_ = h.options.Factory.CreateLocalCaveat() // Source local handler allocates this unused value before load.
	if e = scope.AwaitFulfilled(); e != nil {
		return nil, e
	}
	added, removed, e := h.options.Print.Write.LineChanges(scope)
	if e != nil {
		return nil, e
	}
	resolver := modelaccounting.RestoredModelResolver{Environment: h.options.OriginalEnv, Settings: h.options.Startup.GetInitialSettings, Global: h.options.Global.Get, SDKBetas: h.options.SDKBetas}
	text, e := h.sessionAccounting.FormatTotalCost(scope, modelaccounting.CostPresentation{NowMillis: h.options.NowMillis, StartMillis: h.options.Print.ProcessStartMillis, LinesAdded: added, LinesRemoved: removed, Dim: false, CanonicalName: resolver.Canonical})
	if e != nil {
		return nil, e
	}
	if e = scope.AwaitFulfilled(); e != nil {
		return nil, e
	}
	system := h.options.Factory.CreateLocalCommandInput("<local-command-stdout>" + text + "</local-command-stdout>")
	if e = scope.AwaitFulfilled(); e != nil {
		return nil, e
	}
	caveat := h.options.Factory.CreateLocalCaveat()
	return &sessioncontroller.ProcessedInput{Messages: []protocol.JSONObject{caveat, user, system}, LocalResult: &sessioncontroller.LocalInputResult{Text: text, StartMillis: start}}, nil
}

func retainSlashPrecedingBlocks(result *sessioncontroller.ProcessedInput, original json.RawMessage) error {
	var input []protocol.JSONObject
	if json.Unmarshal(original, &input) != nil || len(input) < 2 || input[len(input)-1].String("type") != "text" {
		return nil
	}
	for i := range result.Messages {
		message := &result.Messages[i]
		if message.String("type") != "user" {
			continue
		}
		api, present := message.Object("message")
		if !present {
			continue
		}
		meta := string(message.Get("isMeta")) == "true"
		if result.NoQuery && meta || !result.NoQuery && !meta {
			continue
		}
		var body []protocol.JSONObject
		if json.Unmarshal(api.Get("content"), &body) != nil {
			if result.NoQuery {
				body = []protocol.JSONObject{protocol.NewJSONObject(protocol.JSONField{Name: "text", Value: api.Get("content")}, protocol.JSONField{Name: "type", Value: quote("text")})}
			} else {
				continue
			}
		}
		combined := append(append([]protocol.JSONObject(nil), input[:len(input)-1]...), body...)
		api.Set("content", raw(combined))
		message.Set("message", raw(api))
		return nil
	}
	return cap("actual slash preceding content message producer")
}

func inputContainsBase64Image(content json.RawMessage) bool {
	var blocks []protocol.JSONObject
	if json.Unmarshal(content, &blocks) != nil {
		return false
	}
	for _, block := range blocks {
		source, _ := block.Object("source")
		if block.String("type") == "image" && source.String("type") == "base64" {
			return true
		}
	}
	return false
}
