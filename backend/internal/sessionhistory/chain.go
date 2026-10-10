package sessionhistory

import (
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strconv"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

var ErrMalformedSnipCycle = errors.New("session history snip removal contains a parent cycle")
var ErrUnsupportedSnipIterator = errors.New("session history does not yet render Bun noniterable errors for nonempty object metadata")

type TypeError struct{ Message string }

func (failure *TypeError) Error() string     { return failure.Message }
func (failure *TypeError) ErrorName() string { return "TypeError" }
func assistantID(value protocol.JSONObject, expression string) (json.RawMessage, error) {
	raw := value.Get("message")
	if len(raw) == 0 || isNull(raw) {
		name := "undefined"
		if isNull(raw) {
			name = "null"
		}
		return nil, &TypeError{Message: name + " is not an object (evaluating '" + expression + "')"}
	}
	return object(raw).Get("id"), nil
}

func diagnostics(values ...protocol.JSONField) json.RawMessage {
	return encoded(protocol.NewJSONObject(values...))
}
func boolean(value bool) json.RawMessage { return json.RawMessage(strconv.FormatBool(value)) }
func integer(value int) json.RawMessage  { return json.RawMessage(strconv.Itoa(value)) }

func (loader *Loader) date(raw json.RawMessage) (float64, bool, error) {
	if len(raw) == 0 {
		return 0, false, nil
	}
	if raw[0] == '"' {
		units, err := protocol.DecodeJavaScriptJSONString(raw)
		if err != nil {
			return 0, false, err
		}
		return loader.options.ParseDate(units)
	}
	if isNull(raw) || string(raw) == "false" {
		return 0, true, nil
	}
	if string(raw) == "true" {
		return 1, true, nil
	}
	if raw[0] == '-' || raw[0] >= '0' && raw[0] <= '9' {
		value, _ := strconv.ParseFloat(string(raw), 64)
		return math.Trunc(value), !math.IsInf(value, 0) && math.Abs(value) <= 8640000000000000, nil
	}
	converted, err := protocol.JavaScriptStringJSON(raw)
	if err != nil {
		return 0, false, err
	}
	units, err := protocol.DecodeJavaScriptJSONString(converted)
	if err != nil {
		return 0, false, err
	}
	return loader.options.ParseDate(units)
}
func (loader *Loader) preserved(value *Transcript) error {
	lastBoundary, lastSegment := -1, -1
	var segment protocol.JSONObject
	indices := map[string]int{}
	for index, entry := range value.records() {
		indices[entry.uuid] = index
		if compact(entry.value) {
			lastBoundary = index
			if candidate := object(entry.value.Get("compactMetadata")).Get("preservedSegment"); truthy(candidate) {
				segment, lastSegment = object(candidate), index
			}
		}
	}
	if lastSegment < 0 {
		return nil
	}
	live := lastBoundary == lastSegment
	keep := map[string]bool{}
	head, tail, anchor := key(segment.Get("headUuid"), -1), key(segment.Get("tailUuid"), -1), key(segment.Get("anchorUuid"), -1)
	if live {
		seen := map[string]bool{}
		current, exists := value.messages[tail]
		reached := false
		for exists && !seen[current.uuid] {
			seen[current.uuid], keep[current.uuid] = true, true
			if current.uuid == head {
				reached = true
				break
			}
			if !truthy(current.value.Get("parentUuid")) {
				break
			}
			current, exists = value.messages[current.parent]
		}
		if !reached {
			_, tailPresent := value.messages[tail]
			_, headPresent := value.messages[head]
			_, anchorPresent := value.messages[anchor]
			loader.options.OnDiagnostic(Diagnostic{Code: "tengu_relink_walk_broken", Fields: diagnostics(protocol.JSONField{Name: "tailInTranscript", Value: boolean(tailPresent)}, protocol.JSONField{Name: "headInTranscript", Value: boolean(headPresent)}, protocol.JSONField{Name: "anchorInTranscript", Value: boolean(anchorPresent)}, protocol.JSONField{Name: "walkSteps", Value: integer(len(seen))}, protocol.JSONField{Name: "transcriptSize", Value: integer(len(value.messages))})})
			return nil
		}
		if message, exists := value.messages[head]; exists {
			message.value.Set("parentUuid", segment.Get("anchorUuid"))
			message.parent = anchor
			value.messages[head] = message
		}
		for _, message := range value.records() {
			if message.parent == anchor && message.uuid != head {
				message.value.Set("parentUuid", segment.Get("tailUuid"))
				message.parent = tail
				value.messages[message.uuid] = message
			}
		}
		for _, message := range value.records() {
			if !keep[message.uuid] || kind(message.value) != "assistant" {
				continue
			}
			if _, err := assistantID(message.value, "msg.message.usage"); err != nil {
				return err
			}
			api := spread(message.value.Get("message"))
			usage := spread(api.Get("usage"))
			for _, name := range []string{"input_tokens", "output_tokens", "cache_creation_input_tokens", "cache_read_input_tokens"} {
				usage.Set(name, json.RawMessage("0"))
			}
			api.Set("usage", encoded(usage))
			message.value.Set("message", encoded(api))
			value.messages[message.uuid] = message
		}
	}
	for _, message := range value.records() {
		if indices[message.uuid] < lastBoundary && !keep[message.uuid] {
			delete(value.messages, message.uuid)
		}
	}
	return nil
}
func (loader *Loader) snip(value *Transcript) error {
	remove := map[string]bool{}
	for _, message := range value.records() {
		raw := object(message.value.Get("snipMetadata")).Get("removedUuids")
		if !truthy(raw) {
			continue
		}
		items, valid := iterable(raw)
		if !valid {
			name := "{}"
			if candidate := object(raw); candidate.IsObject() && len(encoded(candidate)) > 2 {
				return ErrUnsupportedSnipIterator
			}
			if raw[0] == '-' || raw[0] >= '0' && raw[0] <= '9' {
				name = "number"
			} else if string(raw) == "true" {
				name = "true"
			}
			return &TypeError{Message: name + " is not iterable"}
		}
		if valid {
			for _, raw := range items {
				remove[key(raw, -1)] = true
			}
		}
	}
	if len(remove) == 0 {
		return nil
	}
	parents := map[string]json.RawMessage{}
	removed := 0
	for name := range remove {
		if message, exists := value.messages[name]; exists {
			parents[name] = message.value.Get("parentUuid")
			delete(value.messages, name)
			removed++
		}
	}
	relinked := 0
	for _, message := range value.records() {
		if !truthy(message.value.Get("parentUuid")) || !remove[message.parent] {
			continue
		}
		path, seen := []string{}, map[string]bool{}
		current := message.value.Get("parentUuid")
		for truthy(current) && remove[key(current, -1)] {
			name := key(current, -1)
			if seen[name] {
				return ErrMalformedSnipCycle
			}
			seen[name] = true
			path = append(path, name)
			current = parents[name]
			if current == nil {
				current = json.RawMessage("null")
				break
			}
		}
		for _, name := range path {
			parents[name] = current
		}
		message.value.Set("parentUuid", current)
		message.parent = key(current, -1)
		value.messages[message.uuid] = message
		relinked++
	}
	loader.options.OnDiagnostic(Diagnostic{Code: "tengu_snip_resume_filtered", Fields: diagnostics(protocol.JSONField{Name: "removed_count", Value: integer(removed)}, protocol.JSONField{Name: "relinked_count", Value: integer(relinked)})})
	return nil
}
func (loader *Loader) leaves(value *Transcript) {
	parents, children := map[string]bool{}, map[string]bool{}
	for _, message := range value.records() {
		if !isNull(message.value.Get("parentUuid")) {
			parents[message.parent] = true
		}
		if truthy(message.value.Get("parentUuid")) && (kind(message.value) == "user" || kind(message.value) == "assistant") {
			children[message.parent] = true
		}
	}
	added := map[string]bool{}
	prune, cycle := loader.options.LeafPrune(), false
	for _, terminal := range value.records() {
		if parents[terminal.uuid] {
			continue
		}
		seen := map[string]bool{}
		current, exists := terminal, true
		for exists {
			if seen[current.uuid] {
				cycle = true
				break
			}
			seen[current.uuid] = true
			if kind(current.value) == "user" || kind(current.value) == "assistant" {
				if (!prune || !children[current.uuid]) && !added[current.uuid] {
					added[current.uuid] = true
					value.Leaves = append(value.Leaves, current.uuid)
				}
				break
			}
			if !truthy(current.value.Get("parentUuid")) {
				break
			}
			current, exists = value.messages[current.parent]
		}
	}
	if cycle {
		loader.options.OnDiagnostic(Diagnostic{Code: "tengu_transcript_parent_cycle", Fields: diagnostics()})
	}
}
func (loader *Loader) chain(value *Transcript, leaf record) ([]record, error) {
	chain, seen := []record{}, map[string]bool{}
	current, exists := leaf, true
	for exists {
		if seen[current.uuid] {
			raw := current.value.String("uuid")
			loader.options.OnDiagnostic(Diagnostic{Code: "tengu_chain_parent_cycle", Fields: diagnostics(), Message: "Cycle detected in parentUuid chain at message " + raw + ". Returning partial transcript."})
			break
		}
		seen[current.uuid] = true
		chain = append(chain, current)
		if !truthy(current.value.Get("parentUuid")) {
			break
		}
		current, exists = value.messages[current.parent]
	}
	for left, right := 0, len(chain)-1; left < right; left, right = left+1, right-1 {
		chain[left], chain[right] = chain[right], chain[left]
	}
	return loader.parallel(value, chain, seen)
}
func (loader *Loader) sortByTimestamp(values []record) error {
	var failure error
	sort.SliceStable(values, func(left, right int) bool {
		if failure != nil {
			return false
		}
		result, err := loader.options.Compare(values[left].value.Get("timestamp"), values[right].value.Get("timestamp"))
		failure = err
		return result < 0
	})
	return failure
}
func (loader *Loader) parallel(value *Transcript, chain []record, seen map[string]bool) ([]record, error) {
	assistants := []record{}
	anchors, siblings, results := map[string]record{}, map[string][]record{}, map[string][]record{}
	for _, message := range chain {
		if kind(message.value) == "assistant" {
			assistants = append(assistants, message)
			id, err := assistantID(message.value, "a.message.id")
			if err != nil {
				return nil, err
			}
			if truthy(id) {
				anchors[key(id, -1)] = message
			}
		}
	}
	if len(assistants) == 0 {
		return chain, nil
	}
	for _, message := range value.records() {
		api := object(message.value.Get("message"))
		id := api.Get("id")
		if kind(message.value) == "assistant" {
			var err error
			id, err = assistantID(message.value, "m.message.id")
			if err != nil {
				return nil, err
			}
		}
		if kind(message.value) == "assistant" && truthy(id) {
			name := key(id, -1)
			siblings[name] = append(siblings[name], message)
		} else if kind(message.value) == "user" && truthy(message.value.Get("parentUuid")) {
			if _, err := assistantID(message.value, "m.message.content"); err != nil {
				return nil, err
			}
			items, valid := array(api.Get("content"))
			if !valid {
				continue
			}
			for _, block := range items {
				if isNull(block) {
					return nil, &TypeError{Message: "null is not an object (evaluating 'b.type')"}
				}
				if kind(object(block)) == "tool_result" {
					results[message.parent] = append(results[message.parent], message)
					break
				}
			}
		}
	}
	processed, inserts := map[string]bool{}, map[string][]record{}
	count := 0
	for _, assistant := range assistants {
		id := object(assistant.value.Get("message")).Get("id")
		name := key(id, -1)
		if !truthy(id) || processed[name] {
			continue
		}
		processed[name] = true
		group := siblings[name]
		if len(group) == 0 {
			group = []record{assistant}
		}
		orphaned, toolResults := []record{}, []record{}
		for _, member := range group {
			if !seen[member.uuid] {
				orphaned = append(orphaned, member)
			}
			for _, result := range results[member.uuid] {
				if !seen[result.uuid] {
					toolResults = append(toolResults, result)
				}
			}
		}
		if len(orphaned) == 0 && len(toolResults) == 0 {
			continue
		}
		if err := loader.sortByTimestamp(orphaned); err != nil {
			return nil, err
		}
		if err := loader.sortByTimestamp(toolResults); err != nil {
			return nil, err
		}
		recovered := append(orphaned, toolResults...)
		for _, message := range recovered {
			seen[message.uuid] = true
		}
		count += len(recovered)
		inserts[anchors[name].uuid] = recovered
	}
	if count == 0 {
		return chain, nil
	}
	loader.options.OnDiagnostic(Diagnostic{Code: "tengu_chain_parallel_tr_recovered", Fields: diagnostics(protocol.JSONField{Name: "recovered_count", Value: integer(count)})})
	recovered := []record{}
	for _, message := range chain {
		recovered = append(recovered, message)
		recovered = append(recovered, inserts[message.uuid]...)
	}
	return recovered, nil
}
