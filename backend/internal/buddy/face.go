package buddy

import "github.com/NanmiCoder/cc-haha/backend/internal/protocol"

func Face(bones protocol.JSONObject) string {
	eye := bones.String("eye")
	switch bones.String("species") {
	case "duck", "goose":
		return "(" + eye + ">"
	case "blob":
		return "(" + eye + eye + ")"
	case "cat":
		return "=" + eye + "ω" + eye + "="
	case "dragon":
		return "<" + eye + "~" + eye + ">"
	case "octopus":
		return "~(" + eye + eye + ")~"
	case "owl":
		return "(" + eye + ")(" + eye + ")"
	case "penguin":
		return "(" + eye + ">)"
	case "turtle":
		return "[" + eye + "_" + eye + "]"
	case "snail":
		return eye + "(@)"
	case "ghost":
		return "/" + eye + eye + "\\"
	case "axolotl":
		return "}" + eye + "." + eye + "{"
	case "capybara":
		return "(" + eye + "oo" + eye + ")"
	case "cactus":
		return "|" + eye + "  " + eye + "|"
	case "robot":
		return "[" + eye + eye + "]"
	case "rabbit":
		return "(" + eye + ".." + eye + ")"
	case "mushroom":
		return "|" + eye + "  " + eye + "|"
	case "chonk":
		return "(" + eye + "." + eye + ")"
	}
	return ""
}
