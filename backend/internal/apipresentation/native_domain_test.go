package apipresentation

import (
	"reflect"
	"testing"
)

func TestNativePresentationAdmitsActualPolicyConsumersAndKeepsFiniteGuard(t *testing.T) {
	fast := false
	env := originalDomainEnv()
	env["ANTHROPIC_DEFAULT_SONNET_MODEL"] = "deepseek-v4-flash"
	env["ANTHROPIC_DEFAULT_SONNET_MODEL_SUPPORTED_CAPABILITIES"] = "thinking,adaptive_thinking,effort"
	env["CLAUDE_CODE_EFFORT_LEVEL"] = "high"
	env["CLAUDE_CODE_PROVIDER_MAX_OUTPUT_TOKENS"] = "4000"
	before := map[string]string{}
	for k, v := range env {
		before[k] = v
	}
	if _, e := NativeDomainFromOriginalEnvAndFastMode(env, &fast); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(env, before) {
		t.Fatal("original environment mutated")
	}
	if _, e := DomainFromOriginalEnvAndFastMode(env, &fast); e == nil {
		t.Fatal("finite domain was broadened")
	}
	env["PRIVATE_UNCONSUMED_POLICY"] = "true"
	if _, e := NativeDomainFromOriginalEnvAndFastMode(env, &fast); e == nil {
		t.Fatal("unknown policy admitted")
	}
}

func TestNativePresentationBearerIsActualCredentialAndFastTLSGuardsRemain(t *testing.T) {
	fast := false
	env := originalDomainEnv()
	delete(env, "ANTHROPIC_API_KEY")
	env["ANTHROPIC_AUTH_TOKEN"] = "offline-direct-bearer"
	if _, e := NativeDomainFromOriginalEnvAndFastMode(env, &fast); e != nil {
		t.Fatal(e)
	}
	if _, e := DomainFromOriginalEnvAndFastMode(env, &fast); e == nil {
		t.Fatal("finite auth broadened")
	}
	fast = true
	if _, e := NativeDomainFromOriginalEnvAndFastMode(env, &fast); e == nil {
		t.Fatal("active fast mode admitted")
	}
	fast = false
	env["NODE_OPTIONS"] = "--use-system-ca"
	if _, e := NativeDomainFromOriginalEnvAndFastMode(env, &fast); e == nil {
		t.Fatal("unimplemented TLS mode admitted")
	}
}
