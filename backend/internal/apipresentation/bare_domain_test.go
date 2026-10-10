package apipresentation

import (
	"errors"
	"testing"
)

func TestOriginalBareModeIsHostOnlyDomainInput(t *testing.T) {
	env := originalDomainEnv()
	env["CLAUDE_CODE_SIMPLE"] = "1"
	domain, err := DomainFromOriginalEnv(env)
	if err != nil || !domain.direct || env["CLAUDE_CODE_SIMPLE"] != "1" {
		t.Fatalf("original bare direct host not retained: %+v %v", domain, err)
	}
	for _, value := range []string{"", "0", "true", "TRUE", " 1"} {
		env["CLAUDE_CODE_SIMPLE"] = value
		_, err := DomainFromOriginalEnv(env)
		var capability *CapabilityError
		if !errors.As(err, &capability) || capability.Boundary != "original bare environment value" {
			t.Fatalf("unported original bare value %q accepted: %v", value, err)
		}
	}
}
