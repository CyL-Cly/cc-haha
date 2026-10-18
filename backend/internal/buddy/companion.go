// Package buddy retains the frozen companion's seeded appearance and artwork.
package buddy

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

var Species = []string{"duck", "goose", "blob", "cat", "dragon", "octopus", "owl", "penguin", "turtle", "snail", "ghost", "axolotl", "capybara", "cactus", "robot", "rabbit", "mushroom", "chonk"}
var Rarities = []string{"common", "uncommon", "rare", "epic", "legendary"}
var Stats = []string{"DEBUGGING", "PATIENCE", "CHAOS", "WISDOM", "SNARK"}
var eyes = []string{"·", "✦", "×", "◉", "@", "°"}
var hats = []string{"none", "crown", "tophat", "propeller", "halo", "wizard", "beanie", "tinyduck"}

func quote(value any) json.RawMessage { data, _ := json.Marshal(value); return data }
func field(name string, value any) protocol.JSONField {
	return protocol.JSONField{Name: name, Value: quote(value)}
}

func random(seed uint32) func() float64 {
	a := seed
	return func() float64 {
		a += 0x6d2b79f5
		t := (a ^ (a >> 15)) * (1 | a)
		t = (t + (t^(t>>7))*(61|t)) ^ t
		return float64(t^(t>>14)) / 4294967296
	}
}

// Roll's input stays a JS string until Bun's own UTF8 hash boundary. Salt is
// used only by the account/user-id preview, never a stored appearanceSeed.
func Roll(seed json.RawMessage, salted bool) (protocol.JSONObject, int64, error) {
	units, err := protocol.DecodeJavaScriptJSONString(seed)
	if err != nil {
		return protocol.JSONObject{}, 0, err
	}
	if salted {
		units = append(units, utf16.Encode([]rune("friend-2026-401"))...)
	}
	rng := random(uint32(projectpath.SourceHashBytes([]byte(string(utf16.Decode(units))))))
	pick := func(values []string) string { return values[int(math.Floor(rng()*float64(len(values))))] }
	rarity := "legendary"
	roll := rng() * 100
	for i, weight := range []float64{60, 25, 10, 4, 1} {
		roll -= weight
		if roll < 0 {
			rarity = Rarities[i]
			break
		}
	}
	species, eye := pick(Species), pick(eyes)
	hat := "none"
	if rarity != "common" {
		hat = pick(hats)
	}
	shiny := rng() < .01
	floor := map[string]int{"common": 5, "uncommon": 15, "rare": 25, "epic": 35, "legendary": 50}[rarity]
	peak, dump := pick(Stats), pick(Stats)
	for dump == peak {
		dump = pick(Stats)
	}
	// Each stat draws exactly once in its selected Source branch.
	stats := protocol.NewJSONObject()
	for _, name := range Stats {
		value := 0
		switch name {
		case peak:
			value = min(100, floor+50+int(math.Floor(rng()*30)))
		case dump:
			value = max(1, floor-10+int(math.Floor(rng()*15)))
		default:
			value = floor + int(math.Floor(rng()*40))
		}
		stats.Set(name, quote(value))
	}
	bones := protocol.NewJSONObject(field("rarity", rarity), field("species", species), field("eye", eye), field("hat", hat), field("shiny", shiny), protocol.JSONField{Name: "stats", Value: rawObject(stats)})
	return bones, int64(math.Floor(rng() * 1e9)), nil
}

func rawObject(value protocol.JSONObject) json.RawMessage {
	data, _ := value.MarshalJSON()
	return data
}

func Get(global protocol.JSONObject) (*protocol.JSONObject, error) {
	raw := global.Get("companion")
	if !truthy(raw) {
		return nil, nil
	}
	var stored protocol.JSONObject
	if err := stored.UnmarshalJSON(raw); err != nil || !stored.IsObject() {
		return nil, fmt.Errorf("Invalid stored companion")
	}
	seed := stored.Get("appearanceSeed")
	if len(seed) == 0 || string(seed) == "null" {
		parts := []json.RawMessage{quote("legacy:")}
		for i, name := range []string{"hatchedAt", "name", "personality"} {
			if i > 0 {
				parts = append(parts, quote(":"))
			}
			part, err := protocol.JavaScriptStringJSON(stored.Get(name))
			if err != nil {
				return nil, err
			}
			parts = append(parts, part)
		}
		var err error
		seed, err = protocol.JoinJavaScriptValuesJSON(parts, "")
		if err != nil {
			return nil, err
		}
	}
	bones, _, err := Roll(seed, false)
	if err != nil {
		return nil, err
	}
	if override := stored.Get("speciesOverride"); truthy(override) {
		bones.Set("species", override)
	}
	result := stored.Clone()
	for _, property := range bones.OwnProperties() {
		if err = result.SetPropertyJSON(property.NameJSON, property.Value); err != nil {
			return nil, err
		}
	}
	return &result, nil
}

func truthy(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	switch string(raw) {
	case "null", "false", `""`:
		return false
	}
	var n float64
	if json.Unmarshal(raw, &n) == nil {
		return n != 0 && !math.IsNaN(n)
	}
	return true
}

func UserID(global protocol.JSONObject) json.RawMessage {
	account, _ := global.Object("oauthAccount")
	if id := account.Get("accountUuid"); len(id) > 0 && string(id) != "null" {
		return id
	}
	if id := global.Get("userID"); len(id) > 0 && string(id) != "null" {
		return id
	}
	return quote("anon")
}

func Sprite(bones protocol.JSONObject, frame int) ([]string, error) {
	frames := bodies[bones.String("species")]
	if len(frames) == 0 {
		return nil, fmt.Errorf("Unknown companion species")
	}
	if frame < 0 {
		frame = 0
	}
	body := frames[frame%len(frames)]
	lines := make([]string, len(body))
	for i, line := range body {
		lines[i] = strings.ReplaceAll(line, "{E}", bones.String("eye"))
	}
	if bones.String("hat") != "none" && strings.TrimSpace(lines[0]) == "" {
		lines[0] = hatLines[bones.String("hat")]
	}
	allBlank := true
	for _, f := range frames {
		allBlank = allBlank && strings.TrimSpace(f[0]) == ""
	}
	if strings.TrimSpace(lines[0]) == "" && allBlank {
		lines = lines[1:]
	}
	return lines, nil
}

func FrameCount(species string) int { return len(bodies[species]) }
func RarityStars(rarity string) string {
	for i, value := range Rarities {
		if value == rarity {
			return strings.Repeat("★", i+1)
		}
	}
	return ""
}
func RarityRole(rarity string) string {
	return map[string]string{"common": "inactive", "uncommon": "success", "rare": "permission", "epic": "autoAccept", "legendary": "warning"}[rarity]
}
