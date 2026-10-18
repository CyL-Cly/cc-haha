package buddy

import (
	"encoding/json"
	"math"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/webfetch"
)

type Action struct {
	Global *protocol.JSONObject
	Done   bool
	Output json.RawMessage
	PetAt  *float64
}

func text(parts ...json.RawMessage) json.RawMessage {
	values := make([]json.RawMessage, len(parts))
	for i, part := range parts {
		values[i], _ = protocol.JavaScriptStringJSON(part)
	}
	value, _ := protocol.JoinJavaScriptValuesJSON(values, "")
	return value
}

// Apply is the original CompanionCard mount effect, using only the same clock
// and random primitives. It returns no global write for the card/info paths.
func Apply(global protocol.JSONObject, args string, now func() float64, rng func() float64) (Action, error) {
	companion, err := Get(global)
	if err != nil {
		return Action{}, err
	}
	name := quote("Companion")
	rawName := json.RawMessage(nil)
	if companion != nil {
		rawName = companion.Get("name")
		if value := companion.Get("name"); len(value) > 0 && string(value) != "null" {
			name = value
		}
	}
	next := global.Clone()
	switch strings.ToLower(protocol.TrimJavaScriptString(args)) {
	case "mute":
		next.Set("companionMuted", quote(true))
		return Action{Global: &next, Done: true, Output: text(name, quote(" is now muted."))}, nil
	case "unmute":
		next.Set("companionMuted", quote(false))
		return Action{Global: &next, Done: true, Output: text(name, quote(" says hello!"))}, nil
	case "pet":
		if companion == nil {
			return Action{Done: true, Output: quote("You need to hatch a companion first! Use /buddy hatch")}, nil
		}
		timestamp := now()
		return Action{Done: true, Output: text(quote("You pet "), rawName, quote("! ♥")), PetAt: &timestamp}, nil
	case "hatch":
		if companion != nil {
			return Action{Done: true, Output: text(quote("You already have "), rawName, quote("! Use /buddy info to see them."))}, nil
		}
		seed := text(quote("hatch:"), quote(now()), quote(":"), quote(webfetch.RandomBase36Suffix(rng())))
		bones, _, err := Roll(seed, false)
		if err != nil {
			return Action{}, err
		}
		adjectives := []string{"Bright", "Cozy", "Swift", "Calm", "Wise", "Bold", "Fuzzy", "Lucky", "Snappy", "Quirky"}
		nouns := []string{"Spark", "Pixel", "Ember", "Glitch", "Byte", "Flux", "Drift", "Blip", "Quip", "Zap"}
		name := adjectives[int(math.Floor(rng()*float64(len(adjectives))))] + " " + nouns[int(math.Floor(rng()*float64(len(nouns))))]
		soul := protocol.NewJSONObject(field("name", name), field("personality", "A "+bones.String("rarity")+" "+bones.String("species")+" who loves debugging and hanging out."), field("hatchedAt", now()), protocol.JSONField{Name: "appearanceSeed", Value: seed})
		next.Set("companion", rawObject(soul))
		return Action{Global: &next, Done: true, Output: quote("✨ You hatched " + name + " the " + bones.String("rarity") + " " + bones.String("species") + "! Say hello!")}, nil
	case "release":
		if companion == nil {
			return Action{Done: true, Output: quote("No companion to release.")}, nil
		}
		next.Delete("companion")
		return Action{Global: &next, Done: true, Output: text(quote("Goodbye, "), rawName, quote("! You'll be missed."))}, nil
	}
	return Action{}, nil
}
