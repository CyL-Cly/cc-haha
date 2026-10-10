package providerproxy

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type ResponsesCollector struct {
	response  protocol.JSONObject
	blocks    map[int]protocol.JSONObject
	arguments map[int]json.RawMessage
	stopped   bool
}

func NewResponsesCollector(model string, clock func() int64) *ResponsesCollector {
	usage := responseRecord(responseField("input_tokens", 0), responseField("output_tokens", 0))
	response := responseRecord(
		responseRawField("id", responseFallbackID(clock)),
		responseField("type", "message"), responseField("role", "assistant"),
		responseField("model", model), responseField("content", []any{}),
		responseField("stop_reason", nil), responseField("stop_sequence", nil),
		responseField("usage", usage),
	)
	return &ResponsesCollector{response: response, blocks: map[int]protocol.JSONObject{}, arguments: map[int]json.RawMessage{}}
}

func (c *ResponsesCollector) Feed(frame []byte) error {
	line := ""
	for _, value := range strings.Split(string(frame), "\n") {
		if strings.HasPrefix(value, "data: ") {
			line = value[6:]
			break
		}
	}
	if line == "" {
		return nil
	}
	var event protocol.JSONObject
	if err := event.UnmarshalJSON([]byte(line)); err != nil {
		return err
	}
	var index int
	_ = json.Unmarshal(event.Get("index"), &index)
	switch event.String("type") {
	case "error":
		body := responseObject(event.Get("error"))
		failure := &ResponseError{Message: body.String("message"), MessageJSON: body.Get("message"), Type: body.String("type"), Code: body.String("code")}
		if failure.Type == "permission_error" {
			failure.Status = 403
		}
		return failure
	case "message_start":
		message := responseObject(event.Get("message"))
		c.response.Set("id", message.Get("id"))
		c.response.Set("model", message.Get("model"))
	case "content_block_start":
		block := responseObject(event.Get("content_block")).Clone()
		c.blocks[index] = block
		if block.String("type") == "tool_use" {
			c.arguments[index] = responseJSON("")
		}
	case "content_block_delta":
		block, ok := c.blocks[index]
		if !ok {
			return responseError("OpenAI Responses delta has no content block")
		}
		delta := responseObject(event.Get("delta"))
		switch delta.String("type") {
		case "input_json_delta":
			previous := c.arguments[index]
			if responseNullish(previous) {
				previous = responseJSON("")
			}
			c.arguments[index] = responseConcat(previous, delta.Get("partial_json"))
		case "text_delta", "thinking_delta", "signature_delta":
			key := strings.TrimSuffix(delta.String("type"), "_delta")
			previous := block.Get(key)
			if responseNullish(previous) {
				previous = responseJSON("")
			}
			converted, _ := protocol.JavaScriptStringJSON(previous)
			block.Set(key, responseConcat(converted, delta.Get(key)))
			c.blocks[index] = block
		}
	case "message_delta":
		delta := responseObject(event.Get("delta"))
		c.response.Set("stop_reason", delta.Get("stop_reason"))
		usage := responseObject(c.response.Get("usage")).Clone()
		next := responseObject(event.Get("usage"))
		usage.Assign(next)
		c.response.Set("usage", responseJSON(usage))
	case "message_stop":
		c.stopped = true
	}
	return nil
}
func (c *ResponsesCollector) Finish() (protocol.JSONObject, error) {
	if !c.stopped || responseNullish(c.response.Get("stop_reason")) {
		failure := responseError("OpenAI Responses stream closed before response.completed")
		failure.Code = "ERR_STREAM_PREMATURE_CLOSE"
		return protocol.JSONObject{}, failure
	}
	indices := []int{}
	for index := range c.blocks {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	content := []protocol.JSONObject{}
	for _, index := range indices {
		block := c.blocks[index].Clone()
		if block.String("type") == "tool_use" {
			if c.response.String("stop_reason") == "max_tokens" {
				continue
			}
			input, err := ParseResponsesToolArguments(c.arguments[index])
			if err != nil {
				return protocol.JSONObject{}, err
			}
			block.Set("input", input)
		}
		content = append(content, block)
	}
	if len(content) == 0 {
		content = append(content, responseRecord(responseField("type", "text"), responseField("text", "")))
	}
	value := c.response.Clone()
	value.Set("content", responseJSON(content))
	return value, nil
}
