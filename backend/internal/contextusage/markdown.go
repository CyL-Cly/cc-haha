package contextusage

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
)

func decimalFixed(value float64, digits int, decimal bool) string {
	negative := value < 0
	value = math.Abs(value)
	ratio := new(big.Rat)
	if decimal {
		ratio.SetString(strconv.FormatFloat(value, 'g', -1, 64))
	} else {
		ratio.SetFloat64(value)
	}
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(digits)), nil)
	numerator := new(big.Int).Mul(ratio.Num(), scale)
	whole, remainder := new(big.Int), new(big.Int)
	whole.QuoRem(numerator, ratio.Denom(), remainder)
	if new(big.Int).Lsh(remainder, 1).Cmp(ratio.Denom()) >= 0 {
		whole.Add(whole, big.NewInt(1))
	}
	text := whole.String()
	if digits > 0 {
		if len(text) <= digits {
			text = strings.Repeat("0", digits+1-len(text)) + text
		}
		text = text[:len(text)-digits] + "." + text[len(text)-digits:]
	}
	if negative {
		text = "-" + text
	}
	return text
}

// formatTokens is the Source en-US compact formatter, followed by .0 removal.
func formatTokens(value float64) string {
	if math.IsNaN(value) {
		return "nan"
	}
	if math.IsInf(value, 0) {
		if value < 0 {
			return "-∞"
		}
		return "∞"
	}
	scale, suffix := 1.0, ""
	for _, unit := range []struct {
		scale  float64
		suffix string
	}{{1e12, "t"}, {1e9, "b"}, {1e6, "m"}, {1e3, "k"}} {
		if math.Abs(value) >= unit.scale {
			scale, suffix = unit.scale, unit.suffix
			break
		}
	}
	text := decimalFixed(value/scale, 1, true)
	if math.Abs(value) < 1000 {
		text = strings.TrimSuffix(text, ".0")
	}
	rounded, _ := strconv.ParseFloat(text, 64)
	if math.Abs(rounded) >= 1000 && suffix != "t" {
		scale *= 1000
		switch suffix {
		case "":
			suffix = "k"
		case "k":
			suffix = "m"
		case "m":
			suffix = "b"
		case "b":
			suffix = "t"
		}
		text = decimalFixed(value/scale, 1, true)
	}
	return strings.Replace(text+suffix, ".0", "", 1)
}

func sourceDisplay(source string, agent bool) string {
	switch source {
	case "projectSettings":
		return "Project"
	case "userSettings":
		return "User"
	case "localSettings":
		return "Local"
	case "flagSettings":
		return "Flag"
	case "policySettings":
		if agent {
			return "Policy"
		}
		return "Managed"
	case "plugin":
		return "Plugin"
	case "built-in":
		return "Built-in"
	}
	if agent {
		return source
	}
	return "undefined"
}

// Markdown formats the same collected context data used by SDK inspection.
func Markdown(data Data, internal bool) string {
	var out strings.Builder
	fmt.Fprintf(&out, "## Context Usage\n\n**Model:** %s  \n**Tokens:** %s / %s (%s%%)\n\n", data.Model, formatTokens(data.Total), formatTokens(data.RawMax), strconv.FormatFloat(data.Percentage, 'f', -1, 64))
	visible := []Category{}
	for _, category := range data.Categories {
		if category.Tokens > 0 && category.Name != "Free space" && category.Name != "Autocompact buffer" {
			visible = append(visible, category)
		}
	}
	if len(visible) > 0 {
		out.WriteString("### Estimated usage by category\n\n| Category | Tokens | Percentage |\n|----------|--------|------------|\n")
		row := func(category Category) {
			fmt.Fprintf(&out, "| %s | %s | %s%% |\n", category.Name, formatTokens(category.Tokens), decimalFixed(category.Tokens/data.RawMax*100, 1, false))
		}
		for _, category := range visible {
			row(category)
		}
		for _, name := range []string{"Free space", "Autocompact buffer"} {
			for _, category := range data.Categories {
				if category.Name == name && category.Tokens > 0 {
					row(category)
					break
				}
			}
		}
		out.WriteString("\n")
	}
	if len(data.MCP) > 0 {
		out.WriteString("### MCP Tools\n\n| Tool | Server | Tokens |\n|------|--------|--------|\n")
		for _, tool := range data.MCP {
			fmt.Fprintf(&out, "| %s | %s | %s |\n", tool.Name, tool.Server, formatTokens(tool.Tokens))
		}
		out.WriteString("\n")
	}
	if internal && data.SystemTools != nil && len(*data.SystemTools) > 0 {
		out.WriteString("### [ANT-ONLY] System Tools\n\n| Tool | Tokens |\n|------|--------|\n")
		for _, tool := range *data.SystemTools {
			fmt.Fprintf(&out, "| %s | %s |\n", tool.Name, formatTokens(tool.Tokens))
		}
		out.WriteString("\n")
	}
	if internal && data.Sections != nil && len(*data.Sections) > 0 {
		out.WriteString("### [ANT-ONLY] System Prompt Sections\n\n| Section | Tokens |\n|---------|--------|\n")
		for _, section := range *data.Sections {
			fmt.Fprintf(&out, "| %s | %s |\n", section.Name, formatTokens(section.Tokens))
		}
		out.WriteString("\n")
	}
	if len(data.Agents) > 0 {
		out.WriteString("### Custom Agents\n\n| Agent Type | Source | Tokens |\n|------------|--------|--------|\n")
		for _, agent := range data.Agents {
			fmt.Fprintf(&out, "| %s | %s | %s |\n", agent.Name, sourceDisplay(agent.Source, true), formatTokens(agent.Tokens))
		}
		out.WriteString("\n")
	}
	if len(data.Memory) > 0 {
		out.WriteString("### Memory Files\n\n| Type | Path | Tokens |\n|------|------|--------|\n")
		for _, file := range data.Memory {
			fmt.Fprintf(&out, "| %s | %s | %s |\n", file.Type, file.Path, formatTokens(file.Tokens))
		}
		out.WriteString("\n")
	}
	if data.Skills != nil && data.Skills.Tokens > 0 && len(data.Skills.Entries) > 0 {
		out.WriteString("### Skills\n\n| Skill | Source | Tokens |\n|-------|--------|--------|\n")
		for _, skill := range data.Skills.Entries {
			fmt.Fprintf(&out, "| %s | %s | %s |\n", skill.Name, sourceDisplay(skill.Source, false), formatTokens(skill.Tokens))
		}
		out.WriteString("\n")
	}
	if internal {
		out.WriteString("### [ANT-ONLY] Message Breakdown\n\n| Category | Tokens |\n|----------|--------|\n")
		for _, row := range []struct {
			name   string
			tokens float64
		}{{"Tool calls", data.Breakdown.ToolCalls}, {"Tool results", data.Breakdown.ToolResults}, {"Attachments", data.Breakdown.Attachments}, {"Assistant messages (non-tool)", data.Breakdown.Assistant}, {"User messages (non-tool-result)", data.Breakdown.User}} {
			fmt.Fprintf(&out, "| %s | %s |\n", row.name, formatTokens(row.tokens))
		}
		out.WriteString("\n")
		if len(data.Breakdown.Tools) > 0 {
			out.WriteString("#### Top Tools\n\n| Tool | Call Tokens | Result Tokens |\n|------|-------------|---------------|\n")
			for _, tool := range data.Breakdown.Tools {
				fmt.Fprintf(&out, "| %s | %s | %s |\n", tool.Name, formatTokens(tool.Call), formatTokens(tool.Result))
			}
			out.WriteString("\n")
		}
		if len(data.Breakdown.AttachmentTypes) > 0 {
			out.WriteString("#### Top Attachments\n\n| Attachment | Tokens |\n|------------|--------|\n")
			for _, attachment := range data.Breakdown.AttachmentTypes {
				fmt.Fprintf(&out, "| %s | %s |\n", attachment.Name, formatTokens(attachment.Tokens))
			}
			out.WriteString("\n")
		}
	}
	return out.String()
}
