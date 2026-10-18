package application

import (
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func terminalMCPParsingDescription(rows []protocol.JSONObject) string {
	lines := []string{}
	for _, scope := range rows {
		errors, warnings := array(scope.Get("errors")), array(scope.Get("warnings"))
		if len(errors) == 0 && len(warnings) == 0 {
			continue
		}
		status := "Contains warnings"
		if len(errors) > 0 {
			status = "Failed to parse"
		}
		lines = append(lines, "["+status+"] "+scope.String("label"), "Location: "+scope.String("path"))
		for _, group := range []struct {
			label  string
			values []protocol.JSONObject
		}{{"Error", errors}, {"Warning", warnings}} {
			for _, failure := range group.values {
				line := "└ [" + group.label + "] "
				if server := obj(failure.Get("mcpErrorMetadata")).String("serverName"); server != "" {
					line += "[" + server + "] "
				}
				if path := failure.String("path"); path != "" {
					line += path + ": "
				}
				lines = append(lines, line+failure.String("message"))
			}
		}
	}
	return strings.Join(lines, "\n")
}
