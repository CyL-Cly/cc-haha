package resumestate

import (
	"encoding/json"
	"fmt"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionresume"
)

func (service *Service) restoreSkills(history []*sessionresume.Message) error {
	for _, message := range history {
		if text(message.Field("type")) != "attachment" {
			continue
		}
		payload := object(message.Field("attachment"))
		if payload.String("type") == "skill_listing" {
			service.mu.Lock()
			service.state.SuppressNextSkillListing = true
			service.mu.Unlock()
		}
		if payload.String("type") != "invoked_skills" {
			continue
		}
		skills, _ := array(payload.Get("skills"))
		for _, raw := range skills {
			name, err := property(raw, "name", "skill.name")
			if err != nil {
				return err
			}
			path := object(raw).Get("path")
			content := object(raw).Get("content")
			if !truthy(name) || !truthy(path) || !truthy(content) {
				continue
			}
			coerced, err := jsString(name)
			if err != nil {
				return err
			}
			keyUnits, _ := units(coerced)
			key := protocol.QuoteJavaScriptUTF16(append([]uint16{':'}, keyUnits...))
			info := protocol.NewJSONObject(protocol.JSONField{Name: "skillName", Value: name}, protocol.JSONField{Name: "skillPath", Value: path}, protocol.JSONField{Name: "content", Value: content}, protocol.JSONField{Name: "invokedAt", Value: encode(service.options.Now())}, protocol.JSONField{Name: "agentId", Value: []byte("null")})
			entry := protocol.NewJSONObject(protocol.JSONField{Name: "key", Value: key}, protocol.JSONField{Name: "value", Value: encode(info)})
			service.mu.Lock()
			index := -1
			for i, item := range service.state.Skills {
				if scalarEqual(item.Get("key"), key) {
					index = i
				}
			}
			if index < 0 {
				service.state.Skills = append(service.state.Skills, entry)
			} else {
				service.state.Skills[index] = entry
			}
			service.mu.Unlock()
		}
	}
	return nil
}
func (service *Service) checkConsistency(history []json.RawMessage) error {
	for index := len(history) - 1; index >= 0; index-- {
		message := object(history[index])
		if _, err := property(history[index], "type", "m.type"); err != nil {
			return err
		}
		if message.String("type") != "system" || message.String("subtype") != "turn_duration" {
			continue
		}
		expected := message.Get("messageCount")
		if expected == nil {
			return nil
		}
		converted, err := coerceNumber(expected)
		if err != nil {
			return err
		}
		service.diagnostic("event", "tengu_resume_consistency_delta", encode(protocol.NewJSONObject(protocol.JSONField{Name: "expected", Value: expected}, protocol.JSONField{Name: "actual", Value: encode(index)}, protocol.JSONField{Name: "delta", Value: numeric(float64(index) - converted)}, protocol.JSONField{Name: "chain_length", Value: encode(len(history))}, protocol.JSONField{Name: "checkpoint_age_entries", Value: encode(len(history) - 1 - index)})))
		return nil
	}
	return nil
}

// ConsumeSkillListingSuppression is called only at the actual skill-listing
// injection gate; reading State does not consume the one-shot state.
func (service *Service) ConsumeSkillListingSuppression() bool {
	service.mu.Lock()
	defer service.mu.Unlock()
	value := service.state.SuppressNextSkillListing
	service.state.SuppressNextSkillListing = false
	return value
}
func (service *Service) setSlug(sid, slug json.RawMessage) {
	service.mu.Lock()
	defer service.mu.Unlock()
	item := protocol.NewJSONObject(protocol.JSONField{Name: "key", Value: sid}, protocol.JSONField{Name: "value", Value: slug})
	for index, prior := range service.state.PlanSlugs {
		if scalarEqual(prior.Get("key"), sid) {
			service.state.PlanSlugs[index] = item
			return
		}
	}
	service.state.PlanSlugs = append(service.state.PlanSlugs, item)
}
func failure(format string, values ...any) error {
	return &sessionresume.Failure{Name: "Error", Message: fmt.Sprintf(format, values...)}
}
