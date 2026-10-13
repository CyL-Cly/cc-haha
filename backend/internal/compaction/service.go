package compaction

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/preflightcore"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"math"
	"regexp"
	"strings"
	"time"
)

const IncompleteResponse = "Compaction interrupted · This may be due to network issues — please try again."
const NotEnoughMessages = "Not enough messages to compact."
const TooLong = "Conversation too long. Press esc twice to go up a few messages and try again."
const retryMarker = "[earlier conversation truncated for compaction retry]"

type SummaryInput struct {
	History      []protocol.JSONObject
	Prompt       string
	CacheSharing bool
	PreTokens    float64
	Budget       *requestretry.APIAttemptBudget
}
type Summary struct {
	Record protocol.JSONObject
	Text   string
}
type HookResult struct{ Instructions, Display string }
type Dependencies struct {
	Factory      *messages.Factory
	UUID         func() string
	Now          func() time.Time
	Summarize    func(context.Context, SummaryInput) (Summary, error)
	PreHook      func(context.Context, string, string) (HookResult, error)
	PostHook     func(context.Context, string, string) (HookResult, error)
	Restore      func(context.Context, []protocol.JSONObject) ([]protocol.JSONObject, error)
	SessionStart func(context.Context) ([]protocol.JSONObject, error)
}
type Input struct {
	History                                []protocol.JSONObject
	Custom, Arm, TranscriptPath, Direction string
	Pivot                                  int
	Auto, SuppressFollowUp                 bool
	Env                                    map[string]string
}
type Result struct {
	History               []protocol.JSONObject
	Boundary              protocol.JSONObject
	Summary               string
	Display               string
	PreTokens, PostTokens float64
}

func Run(ctx context.Context, in Input, dep Dependencies) (Result, error) {
	result := Result{}
	if ctx == nil || dep.Factory == nil || dep.Summarize == nil || dep.UUID == nil || dep.Now == nil {
		return result, errors.New("compaction requires actual factory, lifetime and model producer")
	}
	if err := ctx.Err(); err != nil {
		return result, context.Cause(ctx)
	}
	all := in.History
	summarize, keep := all, []protocol.JSONObject{}
	if in.Direction != "" {
		if in.Pivot < 0 || in.Pivot > len(all) || in.Direction != "from" && in.Direction != "up_to" {
			return result, errors.New("Invalid partial compaction pivot or direction")
		}
		if in.Direction == "up_to" {
			summarize = all[:in.Pivot]
			keep = all[in.Pivot:]
		} else {
			summarize = all[in.Pivot:]
			keep = all[:in.Pivot]
		}
		filtered := []protocol.JSONObject{}
		for _, m := range keep {
			if m.String("type") == "progress" || in.Direction == "up_to" && (boundary(m) || m.String("type") == "user" && string(m.Get("isCompactSummary")) == "true") {
				continue
			}
			filtered = append(filtered, m)
		}
		keep = filtered
	} else {
		projection, e := preflightcore.AfterCompactBoundary(all)
		if e != nil {
			return result, e
		}
		summarize = nil
		for _, record := range projection.Records {
			summarize = append(summarize, record.Record)
		}
	}
	if len(summarize) == 0 {
		if in.Direction == "up_to" {
			return result, errors.New("Nothing to summarize before the selected message.")
		}
		if in.Direction == "from" {
			return result, errors.New("Nothing to summarize after the selected message.")
		}
		return result, errors.New(NotEnoughMessages)
	}
	tokens, e := preflightcore.EstimateContext(all)
	if e != nil {
		return result, e
	}
	result.PreTokens = tokens
	trigger := "manual"
	if in.Auto {
		trigger = "auto"
	}
	pre := HookResult{}
	if dep.PreHook != nil {
		custom := in.Custom
		if in.Direction != "" {
			custom = ""
		}
		pre, e = dep.PreHook(ctx, trigger, custom)
		if e != nil {
			return result, e
		}
	}
	custom := MergeInstructions(in.Custom, pre.Instructions)
	if in.Direction != "" {
		custom = pre.Instructions
		if in.Custom != "" {
			if custom != "" {
				custom += "\n\n"
			}
			custom += "User context: " + in.Custom
		}
	}
	prompt := Prompt(custom, in.Arm, in.Direction)
	api := summarize
	if in.Direction == "from" {
		api = all
	}
	budget := requestretry.NewCompactAttemptBudget(in.Env)
	var response Summary
	for ptl := 0; ; ptl++ {
		response, e = dep.Summarize(ctx, SummaryInput{History: api, Prompt: prompt, CacheSharing: true, PreTokens: tokens, Budget: budget})
		if ctx.Err() != nil {
			return result, context.Cause(ctx)
		}
		// A canceled/synthetic error is never accepted as a successful summary.
		invalid := e != nil || response.Text == "" || string(response.Record.Get("isApiErrorMessage")) == "true"
		if invalid && budget.Remaining() > 0 {
			response, e = dep.Summarize(ctx, SummaryInput{History: api, Prompt: prompt, CacheSharing: false, PreTokens: tokens, Budget: budget})
		}
		if ctx.Err() != nil {
			return result, context.Cause(ctx)
		}
		if e != nil {
			return result, e
		}
		if strings.HasPrefix(response.Text, "Prompt is too long") {
			if ptl >= 3 || budget.Remaining() <= 0 {
				return result, errors.New(TooLong)
			}
			api, e = TruncateHead(api, response.Record, dep.Factory)
			if e != nil {
				return result, e
			}
			if api == nil {
				return result, errors.New(TooLong)
			}
			continue
		}
		if response.Text == "" {
			return result, errors.New("Failed to generate conversation summary - response did not contain valid text content")
		}
		if string(response.Record.Get("isApiErrorMessage")) == "true" || strings.HasPrefix(response.Text, "API Error") {
			return result, errors.New(response.Text)
		}
		break
	}
	attachments := []protocol.JSONObject{}
	if dep.Restore != nil {
		attachments, e = dep.Restore(ctx, keep)
		if e != nil {
			return result, e
		}
	}
	hooks := []protocol.JSONObject{}
	if dep.SessionStart != nil {
		hooks, e = dep.SessionStart(ctx)
		if e != nil {
			return result, e
		}
	}
	if ctx.Err() != nil {
		return result, context.Cause(ctx)
	}
	metadata := protocol.NewJSONObject(protocol.JSONField{Name: "trigger", Value: encode(trigger)}, protocol.JSONField{Name: "preTokens", Value: encode(tokens)})
	last := ""
	for _, m := range all {
		if m.String("type") != "progress" {
			last = m.String("uuid")
		}
	}
	if in.Direction != "" {
		metadata.Set("messagesSummarized", encode(len(summarize)))
		if in.Custom != "" {
			metadata.Set("userContext", encode(in.Custom))
		}
		last = ""
		parent := keep
		if in.Direction == "up_to" {
			parent = all[:in.Pivot]
		}
		for _, m := range parent {
			if m.String("type") != "progress" {
				last = m.String("uuid")
			}
		}
	}
	discovered := discoveredTools(all)
	if len(discovered) > 0 {
		metadata.Set("preCompactDiscoveredTools", encode(discovered))
	}
	b := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: encode("system")}, protocol.JSONField{Name: "subtype", Value: encode("compact_boundary")}, protocol.JSONField{Name: "content", Value: encode("Conversation compacted")}, protocol.JSONField{Name: "isMeta", Value: encode(false)}, protocol.JSONField{Name: "timestamp", Value: encode(dep.Now().UTC().Format("2006-01-02T15:04:05.000Z"))}, protocol.JSONField{Name: "uuid", Value: encode(dep.UUID())}, protocol.JSONField{Name: "level", Value: encode("info")}, protocol.JSONField{Name: "compactMetadata", Value: encode(metadata)})
	if last != "" {
		b.Set("logicalParentUuid", encode(last))
	}
	options := storage.Object{"content": encode(UserSummary(response.Text, in.SuppressFollowUp, in.TranscriptPath, false)), "isCompactSummary": encode(true)}
	if len(keep) == 0 {
		options["isVisibleInTranscriptOnly"] = encode(true)
	} else {
		m := protocol.NewJSONObject(protocol.JSONField{Name: "messagesSummarized", Value: encode(len(summarize))})
		if in.Custom != "" {
			m.Set("userContext", encode(in.Custom))
		}
		m.Set("direction", encode(in.Direction))
		options["summarizeMetadata"] = encode(m)
	}
	summary := messages.OrderedUser(dep.Factory.CreateUser(options))
	if len(keep) > 0 {
		anchor := b.String("uuid")
		if in.Direction == "up_to" {
			anchor = summary.String("uuid")
		}
		metadata.Set("preservedSegment", encode(map[string]string{"headUuid": keep[0].String("uuid"), "anchorUuid": anchor, "tailUuid": keep[len(keep)-1].String("uuid")}))
		b.Set("compactMetadata", encode(metadata))
	}
	post := HookResult{}
	if dep.PostHook != nil {
		post, e = dep.PostHook(ctx, trigger, response.Text)
		if e != nil {
			return result, e
		}
	}
	if ctx.Err() != nil {
		return result, context.Cause(ctx)
	}
	result.Boundary = b
	result.Summary = response.Text
	result.History = append([]protocol.JSONObject{b, summary}, zeroStaleUsage(keep)...)
	result.History = append(result.History, attachments...)
	result.History = append(result.History, hooks...)
	display := []string{}
	if pre.Display != "" && in.Direction == "" {
		display = append(display, pre.Display)
	}
	if post.Display != "" {
		display = append(display, post.Display)
	}
	result.Display = strings.Join(display, "\n")
	result.PostTokens, _ = preflightcore.RoughHistory(result.History)
	return result, nil
}
func encode(v any) json.RawMessage { data, _ := json.Marshal(v); return data }
func boundary(m protocol.JSONObject) bool {
	return m.String("type") == "system" && m.String("subtype") == "compact_boundary"
}
func zeroStaleUsage(keep []protocol.JSONObject) []protocol.JSONObject {
	out := make([]protocol.JSONObject, len(keep))
	for i, m := range keep {
		out[i] = m
		if m.String("type") != "assistant" {
			continue
		}
		api, present := m.Object("message")
		if !present {
			continue
		}
		usage, present := api.Object("usage")
		if !present {
			continue
		}
		usage = usage.Clone()
		for _, field := range []string{"input_tokens", "output_tokens", "cache_creation_input_tokens", "cache_read_input_tokens"} {
			usage.Set(field, encode(0))
		}
		api = api.Clone()
		api.Set("usage", encode(usage))
		out[i] = m.Clone()
		out[i].Set("message", encode(api))
	}
	return out
}

var anthropicTokens = regexp.MustCompile(`(?i)prompt is too long[^0-9]*(\d+)\s*tokens?\s*>\s*(\d+)`)
var openAITokens = regexp.MustCompile(`(?i)maximum context length is\s+(\d+)\s+tokens?\.\s*(?:however,\s*)you requested\s+(\d+)\s+tokens?\b`)

func TruncateHead(history []protocol.JSONObject, response protocol.JSONObject, factory *messages.Factory) ([]protocol.JSONObject, error) {
	if len(history) > 0 && history[0].String("type") == "user" && string(history[0].Get("isMeta")) == "true" {
		api, _ := history[0].Object("message")
		if api.String("content") == retryMarker {
			history = history[1:]
		}
	}
	groups := groupRounds(history)
	if len(groups) < 2 {
		return nil, nil
	}
	drop := max(1, int(math.Floor(float64(len(groups))*.2)))
	parsed := anthropicTokens.FindStringSubmatch(response.String("errorDetails"))
	if parsed == nil {
		parsed = openAITokens.FindStringSubmatch(response.String("errorDetails"))
		if parsed != nil {
			parsed[1], parsed[2] = parsed[2], parsed[1]
		}
	}
	if len(parsed) >= 3 {
		var actual, limit float64
		_ = json.Unmarshal([]byte(strings.ReplaceAll(parsed[1], ",", "")), &actual)
		_ = json.Unmarshal([]byte(strings.ReplaceAll(parsed[2], ",", "")), &limit)
		if actual > limit && limit > 0 {
			estimate, e := preflightcore.RoughHistory(history)
			if e != nil {
				return nil, e
			}
			need := estimate * (1 - limit*.9/actual)
			acc := float64(0)
			drop = 0
			for _, g := range groups {
				tokens, e := preflightcore.RoughHistory(g)
				if e != nil {
					return nil, e
				}
				acc += tokens
				drop++
				if acc >= need {
					break
				}
			}
		}
	}
	drop = min(drop, len(groups)-1)
	out := []protocol.JSONObject{}
	for _, g := range groups[drop:] {
		out = append(out, g...)
	}
	if len(out) > 0 && out[0].String("type") == "assistant" {
		marker := messages.OrderedUser(factory.CreateUser(storage.Object{"content": encode(retryMarker), "isMeta": encode(true)}))
		out = append([]protocol.JSONObject{marker}, out...)
	}
	return out, nil
}
