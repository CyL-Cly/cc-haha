package teamspawn

import (
	"crypto/rand"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io"
	"math/big"
	"strings"
)

//go:embed assets/spinner_verbs.json
var spinnerVerbsJSON []byte

var CompletionVerbs = []string{"Baked", "Brewed", "Churned", "Cogitated", "Cooked", "Crunched", "Sautéed", "Worked"}

func SpinnerVerbs(settings protocol.JSONObject) ([]string, error) {
	var stock []string
	if err := json.Unmarshal(spinnerVerbsJSON, &stock); err != nil {
		return nil, err
	}
	config, exists := settings.Object("spinnerVerbs")
	if !exists {
		return stock, nil
	}
	var custom []string
	if err := json.Unmarshal(config.Get("verbs"), &custom); err != nil {
		return nil, err
	}
	if config.String("mode") == "replace" {
		if len(custom) > 0 {
			return custom, nil
		}
		return stock, nil
	}
	return append(stock, custom...), nil
}

func Sample(values []string) (json.RawMessage, error) {
	if len(values) == 0 {
		return nil, nil
	}
	index, err := rand.Int(rand.Reader, big.NewInt(int64(len(values))))
	if err != nil {
		return nil, err
	}
	return quoted(values[index.Int64()]), nil
}

func GenerateTaskID(random io.Reader) (string, error) {
	if random == nil {
		return "", errors.New("teammate task ID requires explicit random source")
	}
	data := make([]byte, 8)
	if _, err := io.ReadFull(random, data); err != nil {
		return "", err
	}
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	result := make([]byte, 9)
	result[0] = 't'
	for i, value := range data {
		result[i+1] = alphabet[int(value)%len(alphabet)]
	}
	return string(result), nil
}

func UniqueName(base string, members []protocol.JSONObject) string {
	seen := map[string]bool{}
	for _, member := range members {
		seen[strings.ToLower(member.String("name"))] = true
	}
	if !seen[strings.ToLower(base)] {
		return base
	}
	for index := 2; ; index++ {
		name := fmt.Sprintf("%s-%d", base, index)
		if !seen[strings.ToLower(name)] {
			return name
		}
	}
}

func ResolveModel(env, input, agent, leader string, hasDefinition bool, configured json.RawMessage, parse func(string) (string, error), fallback func() (string, error)) (string, error) {
	if parse == nil || fallback == nil {
		return "", errors.New("teammate model requires actual model parser and fallback")
	}
	defaultModel := func() (string, error) {
		var value string
		if err := json.Unmarshal(configured, &value); err == nil {
			if protocol.TrimJavaScriptString(value) != "" {
				return parse(value)
			}
		}
		if leader != "" {
			return leader, nil
		}
		return fallback()
	}
	inherit := func() (string, error) {
		if leader != "" {
			return leader, nil
		}
		return defaultModel()
	}
	if value := protocol.TrimJavaScriptString(env); value != "" && !strings.EqualFold(value, "inherit") {
		return parse(value)
	}
	if value := protocol.TrimJavaScriptString(input); value != "" {
		if strings.EqualFold(value, "inherit") {
			return inherit()
		}
		return parse(value)
	}
	if hasDefinition {
		if value := protocol.TrimJavaScriptString(agent); value != "" && !strings.EqualFold(value, "inherit") {
			return parse(value)
		}
		return inherit()
	}
	return defaultModel()
}

type StateOptions struct {
	ID, Name, Team, Prompt, ParentSessionID, Color, OutputFile string
	Model, ToolUseID, SpinnerVerb, PastTenseVerb               json.RawMessage
	PlanRequired                                               bool
	Now                                                        float64
}

func CreateState(o StateOptions) (protocol.JSONObject, error) {
	units, err := protocol.DecodeJavaScriptJSONString(quoted(o.Prompt))
	if err != nil {
		return protocol.JSONObject{}, err
	}
	length := len(units)
	if length > 50 {
		units = units[:50]
	}
	suffix := ""
	if length > 50 {
		suffix = "..."
	}
	description, err := protocol.JoinJavaScriptValuesJSON([]json.RawMessage{quoted(o.Name + ": "), protocol.QuoteJavaScriptUTF16(units), quoted(suffix)}, "")
	if err != nil {
		return protocol.JSONObject{}, err
	}
	identity := protocol.NewJSONObject(protocol.JSONField{Name: "agentId", Value: quoted(o.Name + "@" + o.Team)}, protocol.JSONField{Name: "agentName", Value: quoted(o.Name)}, protocol.JSONField{Name: "teamName", Value: quoted(o.Team)}, protocol.JSONField{Name: "color", Value: optionalRaw(o.Color)}, protocol.JSONField{Name: "planModeRequired", Value: rawJSON(o.PlanRequired)}, protocol.JSONField{Name: "parentSessionId", Value: quoted(o.ParentSessionID)})
	mode := "default"
	if o.PlanRequired {
		mode = "plan"
	}
	return protocol.NewJSONObject(protocol.JSONField{Name: "id", Value: quoted(o.ID)}, protocol.JSONField{Name: "type", Value: quoted("in_process_teammate")}, protocol.JSONField{Name: "status", Value: quoted("running")}, protocol.JSONField{Name: "description", Value: description}, protocol.JSONField{Name: "toolUseId", Value: o.ToolUseID}, protocol.JSONField{Name: "startTime", Value: rawJSON(o.Now)}, protocol.JSONField{Name: "outputFile", Value: quoted(o.OutputFile)}, protocol.JSONField{Name: "outputOffset", Value: rawJSON(0)}, protocol.JSONField{Name: "notified", Value: rawJSON(false)}, protocol.JSONField{Name: "identity", Value: rawJSON(identity)}, protocol.JSONField{Name: "prompt", Value: quoted(o.Prompt)}, protocol.JSONField{Name: "model", Value: o.Model}, protocol.JSONField{Name: "awaitingPlanApproval", Value: rawJSON(false)}, protocol.JSONField{Name: "spinnerVerb", Value: o.SpinnerVerb}, protocol.JSONField{Name: "pastTenseVerb", Value: o.PastTenseVerb}, protocol.JSONField{Name: "permissionMode", Value: quoted(mode)}, protocol.JSONField{Name: "isIdle", Value: rawJSON(false)}, protocol.JSONField{Name: "shutdownRequested", Value: rawJSON(false)}, protocol.JSONField{Name: "lastReportedToolCount", Value: rawJSON(0)}, protocol.JSONField{Name: "lastReportedTokenCount", Value: rawJSON(0)}, protocol.JSONField{Name: "pendingUserMessages", Value: rawJSON([]string{})}, protocol.JSONField{Name: "messages", Value: rawJSON([]protocol.JSONObject{})}), nil
}

func optionalRaw(value string) json.RawMessage {
	if value == "" {
		return nil
	}
	return quoted(value)
}
