package tui

import (
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/contextusage"
	"strings"
)

// ContextVisualization colors only known analyser categories. All model/path
// text still crosses the normal terminal rendering boundary.
func ContextVisualization(data contextusage.Data, width int) []string {
	lines := []string{"Context Usage"}
	colors := map[string]string{"System prompt": "\x1b[35m", "System tools": "\x1b[36m", "MCP tools": "\x1b[34m", "Messages": "\x1b[33m", "Memory files": "\x1b[32m", "Custom agents": "\x1b[31m", "Skills": "\x1b[35m", "Autocompact buffer": "\x1b[90m"}
	for _, row := range contextusage.PresentationGrid(data, width) {
		var line strings.Builder
		for _, square := range row {
			glyph := "⛁"
			color := colors[square.Name]
			if square.Fullness < .7 {
				glyph = "⛀"
			}
			if square.Name == "Free space" {
				glyph, color = "⛶", "\x1b[90m"
			}
			if square.Name == "Autocompact buffer" {
				glyph = "⛝"
			}
			if color == "" {
				color = "\x1b[36m"
			}
			line.WriteString(color + glyph + "\x1b[0m ")
		}
		lines = append(lines, line.String())
	}
	lines = append(lines, wrap(fmt.Sprintf("%s · %.0f/%.0f tokens (%g%%)", data.Model, data.Total, data.RawMax, data.Percentage), width)...)
	lines = append(lines, "Estimated usage by category")
	for _, category := range data.Categories {
		if category.Tokens <= 0 {
			continue
		}
		percentage := fmt.Sprintf("%.1f%%", category.Tokens/data.RawMax*100)
		if category.Deferred {
			percentage = "N/A"
		}
		lines = append(lines, wrap(fmt.Sprintf("%s: %.0f tokens (%s)", category.Name, category.Tokens, percentage), width)...)
	}
	for _, file := range data.Memory {
		lines = append(lines, wrap(fmt.Sprintf("Memory · %s · %s: %.0f tokens", file.Type, file.Path, file.Tokens), width)...)
	}
	for _, tool := range data.MCP {
		lines = append(lines, wrap(fmt.Sprintf("MCP tool · %s · %s: %.0f tokens", tool.Server, tool.Name, tool.Tokens), width)...)
	}
	for _, agent := range data.Agents {
		lines = append(lines, wrap(fmt.Sprintf("Custom agent · %s · %s: %.0f tokens", agent.Source, agent.Name, agent.Tokens), width)...)
	}
	if data.Skills != nil {
		for _, skill := range data.Skills.Entries {
			lines = append(lines, wrap(fmt.Sprintf("Skill · %s · %s: %.0f tokens", skill.Source, skill.Name, skill.Tokens), width)...)
		}
	}
	return lines
}
