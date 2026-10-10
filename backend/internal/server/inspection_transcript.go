package server

import (
	"context"
	"encoding/json"
	"math"
	"path/filepath"
	"runtime"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/activitystatsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/preflightcore"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolsearch"
	whatwg "github.com/nlnwa/whatwg-url/url"
)

type inspectionTranscriptSnapshot struct {
	LaunchInfo      protocol.JSONObject `json:"launchInfo"`
	Metadata        protocol.JSONObject `json:"metadata"`
	Usage           json.RawMessage     `json:"usage"`
	ContextEstimate json.RawMessage     `json:"contextEstimate"`
}
type inspectionUsageAnchor struct {
	model                      string
	input, output, read, write float64
}
type inspectionModelUsage struct {
	value protocol.JSONObject
	hint  protocol.JSONObject
}

func inspectionNumber(raw json.RawMessage) float64 {
	var n float64
	if string(raw) == "null" || json.Unmarshal(raw, &n) != nil {
		return 0
	}
	return n
}
func inspectionIsString(raw json.RawMessage) bool {
	return len(raw) > 0 && raw[0] == '"'
}
func inspectionNormalizeDrive(raw json.RawMessage) json.RawMessage {
	if runtime.GOOS == "windows" {
		var text string
		_ = json.Unmarshal(raw, &text)
		if len(text) == 2 && text[1] == ':' {
			return jsonString(text + `\`)
		}
	}
	return raw
}
func inspectionMedia(entry protocol.JSONObject) bool {
	kind := entry.String("type")
	if kind == "attachment" {
		attachment := collaborationObject(entry.Get("attachment"))
		content := collaborationObject(attachment.Get("content"))
		return attachment.String("type") == "file" && (content.String("type") == "image" || content.String("type") == "pdf")
	}
	if kind != "user" && kind != "assistant" {
		return false
	}
	for _, block := range contentBlocks(collaborationObject(entry.Get("message")).Get("content")) {
		if stringField(block, "type") == "image" || stringField(block, "type") == "document" {
			return true
		}
	}
	return false
}

// This reducer reads original semantic records, including child/fork context.
// Display projection and billable usage have deliberately different policies.
func (s *Server) readInspectionTranscript(ctx context.Context, info sessionInfo, o modelaccounting.NativeCalculatorOptions) (inspectionTranscriptSnapshot, error) {
	metadata := protocol.NewJSONObject()
	launch := protocol.NewJSONObject()
	launch.Set("filePath", jsonString(info.Path))
	launch.Set("projectDir", jsonString(filepath.Base(filepath.Dir(info.Path))))
	launch.Set("customTitle", []byte("null"))
	var workDir, cwd, effort json.RawMessage
	messageCount := 0
	models := map[string]*inspectionModelUsage{}
	order := []string{}
	counted := map[string]bool{}
	var totalCost, input, output, read, write, search float64
	unknown := false
	var firstAt, lastAt *float64
	var latest *inspectionUsageAnchor
	latestModel := ""
	hasLatestModel := false
	estimated, afterUsage := 0.0, 0.0
	media := false
	factory, _ := messages.NewFactory(func() string { id, _ := uuid(); return id }, time.Now)
	parser := activitystatsapi.NewIndexDateParser(nil)
	limit := int64(8 * 1024 * 1024)
	scan, err := s.reader.Stream(ctx, info.Path, func(raw json.RawMessage, _ bool, _ int64) error {
		var entry protocol.JSONObject
		if err := entry.UnmarshalJSON(raw); err != nil {
			return err
		}
		message := collaborationObject(entry.Get("message"))
		if inspectionIsString(message.Get("model")) {
			metadata.Set("model", message.Get("model"))
			latestModel = message.String("model")
			hasLatestModel = true
		}
		if inspectionIsString(entry.Get("cwd")) {
			metadata.Set("cwd", entry.Get("cwd"))
			cwd = inspectionNormalizeDrive(entry.Get("cwd"))
		}
		if inspectionIsString(entry.Get("version")) {
			metadata.Set("version", entry.Get("version"))
		}
		if entry.String("type") == "session-meta" {
			if inspectionIsString(entry.Get("workDir")) {
				workDir = inspectionNormalizeDrive(entry.Get("workDir"))
			}
			if mode := entry.String("permissionMode"); mode == "default" || mode == "acceptEdits" || mode == "bypassPermissions" || mode == "plan" || mode == "dontAsk" || mode == "auto" {
				launch.Set("permissionMode", entry.Get("permissionMode"))
			}
			if raw := entry.Get("runtimeProviderId"); string(raw) == "null" || inspectionIsString(raw) {
				launch.Set("runtimeProviderId", raw)
			}
			if inspectionIsString(entry.Get("runtimeModelId")) {
				launch.Set("runtimeModelId", entry.Get("runtimeModelId"))
			}
			effort = sessionprojection.ResolveSessionEffortLevel(entry, effort)
		}
		if raw := entry.Get("repository"); len(raw) > 0 && string(raw) != "null" && (raw[0] == '{' || raw[0] == '[') {
			launch.Set("repository", raw)
		}
		if entry.String("type") == "worktree-state" {
			raw := entry.Get("worktreeSession")
			value := collaborationObject(raw)
			if string(raw) == "null" || value.IsObject() && inspectionIsString(value.Get("worktreePath")) && inspectionIsString(value.Get("worktreeName")) {
				launch.Set("worktreeSession", raw)
			}
		}
		if entry.String("type") == "custom-title" && inspectionIsString(entry.Get("customTitle")) {
			launch.Set("customTitle", entry.Get("customTitle"))
		}
		kind := entry.String("type")
		if !sourceJSONTruthy(entry.Get("isMeta")) && sourceJSONTruthy(message.Get("role")) && (kind == "user" || kind == "assistant" || kind == "system") {
			messageCount++
		}
		if kind == "system" && entry.String("subtype") == "compact_boundary" {
			latest = nil
			estimated = 0
			afterUsage = 0
			media = false
		}
		if kind == "user" || kind == "assistant" || kind == "attachment" {
			tokens, e := preflightcore.RoughHistoryWithAttachments([]protocol.JSONObject{entry}, &preflightcore.AttachmentOptions{Factory: factory})
			if e != nil {
				return e
			}
			estimated += tokens
			afterUsage += tokens
			media = media || inspectionMedia(entry)
		}
		usage := collaborationObject(message.Get("usage"))
		if !usage.IsObject() || !inspectionIsString(message.Get("model")) {
			return nil
		}
		in, out, rd, wr := inspectionNumber(usage.Get("input_tokens")), inspectionNumber(usage.Get("output_tokens")), inspectionNumber(usage.Get("cache_read_input_tokens")), inspectionNumber(usage.Get("cache_creation_input_tokens"))
		if in != 0 || out != 0 || rd != 0 || wr != 0 {
			latest = &inspectionUsageAnchor{message.String("model"), in, out, rd, wr}
			afterUsage = 0
		}
		if !activitystatsapi.IndexBillableUsage(entry) {
			return nil
		}
		if key, ok := activitystatsapi.IndexUsageKey(entry); ok {
			if counted[key] {
				return nil
			}
			if len(counted) >= 50000 || len(utf16.Encode([]rune(key))) > 4096 {
				return &apiError{413, "HISTORY_INSPECTION_LIMIT", "Usage inspection exceeds its record budget"}
			}
			counted[key] = true
		}
		web := inspectionNumber(collaborationObject(usage.Get("server_tool_use")).Get("web_search_requests"))
		if in == 0 && out == 0 && rd == 0 && wr == 0 && web == 0 {
			return nil
		}
		model := message.String("model")
		if _, ok := models[model]; !ok && (len(models) >= 64 || len(utf16.Encode([]rune(model))) > 512) {
			return &apiError{413, "HISTORY_INSPECTION_LIMIT", "Usage inspection exceeds its model budget"}
		}
		cost, e := modelaccounting.TranscriptPricing(model, usage, o)
		if e != nil {
			return e
		}
		row := models[model]
		if row == nil {
			hint := protocol.NewJSONObject()
			for _, key := range []string{"runtimeProviderId", "runtimeModelId"} {
				if launch.Get(key) != nil {
					hint.Set(key, launch.Get(key))
				}
			}
			value := protocol.NewJSONObject()
			value.Set("model", message.Get("model"))
			value.Set("displayName", jsonString(cost.DisplayName))
			for _, key := range []string{"inputTokens", "outputTokens", "cacheReadInputTokens", "cacheCreationInputTokens", "webSearchRequests", "costUSD", "contextWindow"} {
				value.Set(key, []byte("0"))
			}
			value.Set("costDisplay", jsonString("$0.0000"))
			value.Set("maxOutputTokens", recoveryJSON(cost.MaxOutputTokens))
			row = &inspectionModelUsage{value, hint}
			models[model] = row
			order = append(order, model)
		}
		for key, n := range map[string]float64{"inputTokens": in, "outputTokens": out, "cacheReadInputTokens": rd, "cacheCreationInputTokens": wr, "webSearchRequests": web, "costUSD": cost.CostUSD} {
			row.value.Set(key, recoveryJSON(inspectionNumber(row.value.Get(key))+n))
		}
		row.value.Set("costDisplay", jsonString(modelaccounting.FormatUsageCost(inspectionNumber(row.value.Get("costUSD")))))
		totalCost += cost.CostUSD
		input += in
		output += out
		read += rd
		write += wr
		search += web
		unknown = unknown || cost.HasUnknownModelCost
		if raw := entry.Get("timestamp"); sourceJSONTruthy(raw) {
			text, e := protocol.ExactJavaScriptStringJSON(raw)
			if e != nil {
				return e
			}
			ms, valid, e := parser.Parse(stringField(historyObject{"time": text}, "time"))
			if e != nil {
				return e
			}
			if valid {
				if firstAt == nil || ms < *firstAt {
					copy := ms
					firstAt = &copy
				}
				if lastAt == nil || ms > *lastAt {
					copy := ms
					lastAt = &copy
				}
			}
		}
		return nil
	}, sessionstore.StreamOptions{MaxRecordBytes: &limit})
	if err != nil {
		return inspectionTranscriptSnapshot{}, err
	}
	if scan.OversizedRecords > 0 {
		return inspectionTranscriptSnapshot{}, &apiError{413, "HISTORY_INSPECTION_LIMIT", "Transcript inspection contains records above the semantic read limit"}
	}
	chosen := stringField(historyObject{"v": workDir}, "v")
	if chosen == "" {
		chosen = stringField(historyObject{"v": cwd}, "v")
	}
	if chosen == "" {
		chosen = desanitizeSessionProject(filepath.Base(filepath.Dir(info.Path)))
	}
	if chosen == "" {
		chosen = s.options.WorkDir
	}
	launch.Set("workDir", jsonString(chosen))
	launch.Set("transcriptMessageCount", recoveryJSON(messageCount))
	if sourceJSONTruthy(effort) {
		launch.Set("effortLevel", effort)
	}
	if !sourceJSONTruthy(launch.Get("runtimeModelId")) {
		launch.Delete("runtimeModelId")
	}
	result := inspectionTranscriptSnapshot{LaunchInfo: launch, Metadata: metadata, Usage: []byte("null"), ContextEstimate: []byte("null")}
	modelRows := []protocol.JSONObject{}
	for _, model := range order {
		row := models[model]
		window, e := s.inspectionContextWindow(ctx, model, row.hint, o)
		if e != nil {
			return inspectionTranscriptSnapshot{}, e
		}
		row.value.Set("contextWindow", recoveryJSON(window))
		modelRows = append(modelRows, row.value)
	}
	if len(order) > 0 {
		duration := 0.0
		if firstAt != nil && lastAt != nil {
			duration = math.Max(0, math.Floor((*lastAt-*firstAt)/1000+0.5))
		}
		result.Usage = recoveryJSON(map[string]any{"source": "transcript", "totalCostUSD": totalCost, "costDisplay": modelaccounting.FormatUsageCost(totalCost), "hasUnknownModelCost": unknown, "totalAPIDuration": 0, "totalDecodeDuration": 0, "totalTtftDuration": 0, "totalDuration": duration, "totalLinesAdded": 0, "totalLinesRemoved": 0, "totalInputTokens": input, "totalOutputTokens": output, "totalCacheReadInputTokens": read, "totalCacheCreationInputTokens": write, "totalWebSearchRequests": search, "models": modelRows})
	}
	if latest == nil && hasLatestModel {
		latest = &inspectionUsageAnchor{model: latestModel}
	}
	if latest != nil {
		window, e := s.inspectionContextWindow(ctx, latest.model, launch, o)
		if e != nil {
			return inspectionTranscriptSnapshot{}, e
		}
		result.ContextEstimate = inspectionContextEstimate(*latest, estimated, afterUsage, media, window, o.Environment)
	}
	return result, nil
}

// Bound below to the original saved-provider preference and model resolvers.
func (s *Server) inspectionContextWindow(ctx context.Context, model string, hint protocol.JSONObject, o modelaccounting.NativeCalculatorOptions) (float64, error) {
	if s.options.InspectionContextWindow != nil {
		return s.options.InspectionContextWindow(ctx, model, hint)
	}
	return s.resolveInspectionContextWindow(ctx, model, hint, o)
}

func inspectionContextEstimate(latest inspectionUsageAnchor, estimated, after float64, media bool, window float64, env map[string]string) json.RawMessage {
	prompt := latest.input + latest.read + latest.write
	provider := prompt + latest.output
	if estimated == 0 {
		estimated = prompt
	}
	base := env["ANTHROPIC_BASE_URL"]
	trusted := base == ""
	if base != "" {
		parsed, err := whatwg.Parse(base)
		trusted = err == nil && (parsed.Host() == "api.anthropic.com" || env["USER_TYPE"] == "ant" && parsed.Host() == "api-staging.anthropic.com")
	}
	trusted = trusted && toolsearch.Provider(env) == "firstParty"
	ignored := !trusted && media && estimated > 0 && provider >= window && estimated < window && provider > math.Max(estimated*4, estimated+50000)
	total := math.Min(math.Max(estimated, provider), window)
	if ignored {
		total = math.Min(estimated, window)
	} else if provider > 0 {
		total = math.Min(math.Max(total, provider+after), window)
	}
	categories := []map[string]any{}
	if total == provider {
		categories = []map[string]any{{"name": "Input tokens", "tokens": latest.input, "color": "#8f3217"}, {"name": "Cache read", "tokens": latest.read, "color": "#0f5c8f"}, {"name": "Cache write", "tokens": latest.write, "color": "#7c3aed"}, {"name": "Output tokens", "tokens": latest.output, "color": "#2f7d32"}}
	} else {
		categories = append(categories, map[string]any{"name": "Estimated context", "tokens": total, "color": "#8f3217"})
	}
	categories = append(categories, map[string]any{"name": "Free space", "tokens": math.Max(0, window-total), "color": "#a1a1aa", "isDeferred": true})
	visible := []map[string]any{}
	for _, category := range categories {
		if category["tokens"].(float64) > 0 {
			visible = append(visible, category)
		}
	}
	filled := int(math.Max(0, math.Min(100, math.Floor(total/math.Max(1, window)*100+0.5))))
	grid := [][]map[string]any{}
	for row := 0; row < 10; row++ {
		cells := []map[string]any{}
		for col := 0; col < 10; col++ {
			color, name, fullness := "#a1a1aa", "Free space", 0
			if row*10+col < filled {
				color, name, fullness = "#8f3217", "Input context", 1
			}
			cells = append(cells, map[string]any{"color": color, "isFilled": fullness == 1, "categoryName": name, "tokens": math.Floor(window/100 + 0.5), "percentage": 1, "squareFullness": fullness})
		}
		grid = append(grid, cells)
	}
	percent := 0.0
	if window > 0 {
		percent = math.Floor(total/window*100 + 0.5)
	}
	return recoveryJSON(map[string]any{"categories": visible, "totalTokens": total, "maxTokens": window, "rawMaxTokens": window, "percentage": percent, "gridRows": grid, "model": latest.model, "memoryFiles": []any{}, "mcpTools": []any{}, "agents": []any{}, "apiUsage": map[string]any{"input_tokens": latest.input, "output_tokens": latest.output, "cache_creation_input_tokens": latest.write, "cache_read_input_tokens": latest.read}})
}
