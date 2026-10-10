package uipreferences

import (
	"encoding/json"
	"math"
	"regexp"
	"strconv"
)

const CurrentSchemaVersion = 6

var petIDPattern = regexp.MustCompile(`^(?:custom:)?[a-z0-9]+(?:-[a-z0-9]+)*$`)
var avatarPattern = regexp.MustCompile(`^profile/avatar\.(png|jpg|webp)$`)

func normalizeSidebar(raw json.RawMessage) object {
	value, _ := parseObject(raw)
	for _, key := range []string{"projectOrder", "pinnedProjects", "hiddenProjects"} {
		value.set(key, normalizedStringArray(value.get(key)))
	}
	value.set("projectOrganization", normalizedChoice(value.get("projectOrganization"), []string{"project", "recentProject", "time"}, "recentProject"))
	value.set("projectSortBy", normalizedChoice(value.get("projectSortBy"), []string{"createdAt", "updatedAt"}, "updatedAt"))
	return value
}
func normalizedText(raw json.RawMessage, fallback string, max int) json.RawMessage {
	value, valid := units(raw)
	if !valid {
		return quoted(fallback)
	}
	value = collapse(value)
	if len(value) == 0 {
		return quoted(fallback)
	}
	if len(value) > max {
		value = value[:max]
	}
	return quotedUnits(value)
}
func normalizedAvatar(raw json.RawMessage) json.RawMessage {
	value, valid := units(raw)
	if !valid {
		return json.RawMessage("null")
	}
	var path string
	_ = json.Unmarshal(quotedUnits(value), &path)
	if !avatarPattern.MatchString(path) {
		return json.RawMessage("null")
	}
	return quoted(path)
}
func normalizeProfile(raw json.RawMessage) object {
	value, _ := parseObject(raw)
	value.set("displayName", normalizedText(value.get("displayName"), "cc-haha", 80))
	value.set("subtitle", normalizedText(value.get("subtitle"), "github.com/NanmiCoder/cc-haha", 160))
	value.set("avatarFile", normalizedAvatar(value.get("avatarFile")))
	if _, valid := units(value.get("avatarUpdatedAt")); !valid {
		value.set("avatarUpdatedAt", json.RawMessage("null"))
	}
	return value
}
func normalizedPetID(raw json.RawMessage) json.RawMessage {
	value, valid := units(raw)
	if !valid {
		return quoted("dada-code")
	}
	value = trim(value)
	if len(value) == 0 || len(value) > 80 {
		return quoted("dada-code")
	}
	var candidate string
	_ = json.Unmarshal(quotedUnits(value), &candidate)
	if !petIDPattern.MatchString(candidate) {
		return quoted("dada-code")
	}
	return quotedUnits(value)
}
func normalizePet(raw json.RawMessage) object {
	value, _ := parseObject(raw)
	value.set("enabled", boolOr(value.get("enabled"), false))
	value.set("selectedPetId", normalizedPetID(value.get("selectedPetId")))
	size, valid := number(value.get("size"))
	if !valid || math.IsInf(size, 0) || math.Trunc(size) != size {
		size = 144
	} else {
		size = math.Min(192, math.Max(96, size))
	}
	value.set("size", json.RawMessage(strconv.FormatFloat(size, 'f', 0, 64)))
	for _, key := range []string{"showTaskPanel", "collapsed"} {
		value.set(key, boolOr(value.get(key), false))
	}
	value.set("motionEnabled", boolOr(value.get("motionEnabled"), true))
	session, valid := units(value.get("lastSessionId"))
	session = trim(session)
	if !valid || len(session) == 0 {
		value.set("lastSessionId", json.RawMessage("null"))
	} else {
		if len(session) > 200 {
			session = session[:200]
		}
		value.set("lastSessionId", quotedUnits(session))
	}
	return value
}
func normalizeVoice(raw json.RawMessage) object {
	value, _ := parseObject(raw)
	value.set("enabled", boolOr(value.get("enabled"), false))
	provider, valid := units(value.get("providerId"))
	provider = trim(provider)
	if !valid || len(provider) == 0 || len(provider) > 80 {
		value.set("providerId", quoted("sensevoice-local"))
	} else {
		value.set("providerId", quotedUnits(provider))
	}
	value.set("language", normalizedChoice(value.get("language"), []string{"auto", "zh", "en", "ja", "ko", "yue"}, "auto"))
	value.set("downloadSource", normalizedChoice(value.get("downloadSource"), []string{"auto", "official", "mirror"}, "auto"))
	return value
}
func validProjectKey(raw json.RawMessage) bool {
	value, valid := units(raw)
	return valid && len(value) > 0 && len(value) <= 4096 && len(trim(value)) > 0
}
func displayName(raw json.RawMessage) (json.RawMessage, bool) {
	value, valid := units(raw)
	if !valid {
		return nil, false
	}
	value = collapse(value)
	if len(value) == 0 || len(value) > 80 {
		return nil, false
	}
	return quotedUnits(value), true
}
func normalizeProjectNames(raw json.RawMessage) object {
	value, _ := parseObject(raw)
	result := object{}
	for _, item := range value {
		if !validProjectKey(json.RawMessage(item.key)) {
			continue
		}
		name, valid := displayName(item.value)
		if !valid {
			continue
		}
		result = append(result, property{item.key, name})
		if len(result) >= 2000 {
			break
		}
	}
	return result
}
func normalize(raw json.RawMessage) (Preferences, bool, error) {
	value, valid := parseObject(raw)
	if !valid {
		return nil, false, nil
	}
	schema, valid := number(value.get("schemaVersion"))
	if !valid || math.IsInf(schema, 0) || math.Trunc(schema) != schema || math.Abs(schema) > 9007199254740991 || schema <= CurrentSchemaVersion {
		value.set("schemaVersion", json.RawMessage("6"))
	}
	value.set("sidebar", normalizeSidebar(value.get("sidebar")).raw())
	value.set("profile", normalizeProfile(value.get("profile")).raw())
	value.set("pet", normalizePet(value.get("pet")).raw())
	value.set("voiceInput", normalizeVoice(value.get("voiceInput")).raw())
	value.set("projectDisplayNames", normalizeProjectNames(value.get("projectDisplayNames")).raw())
	return Preferences(value.raw()), true, nil
}
func defaults() Preferences {
	value, _, _ := normalize(json.RawMessage(`{"schemaVersion":6}`))
	return value
}
