package clibootstrap

import (
	"encoding/json"
	"strings"
)

func nativeEffortValue(value string) json.RawMessage {
	if value == "" {
		return nil
	}
	if strings.EqualFold(value, "ultracode") {
		value = "xhigh"
	}
	return quoteMain(value)
}
