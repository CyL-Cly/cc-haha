package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/retrypolicy"
)

type step struct {
	Attempt json.RawMessage `json:"attempt"`
	Header  *string         `json:"header"`
	Maximum json.RawMessage `json:"maximum"`
	Random  json.RawMessage `json:"random"`
}

type failureSpec struct {
	Kind      string          `json:"kind"`
	SDKClass  *string         `json:"sdkClass"`
	Message   string          `json:"message"`
	Code      *string         `json:"code"`
	Payload   json.RawMessage `json:"payload"`
	Cause     *failureSpec    `json:"cause"`
	SelfCause bool            `json:"selfCause"`
}

type fixtureError struct {
	message string
	cause   error
}

func (failure *fixtureError) Error() string { return failure.message }
func (failure *fixtureError) Unwrap() error { return failure.cause }

type codedFixtureError struct {
	*fixtureError
	code string
}

func (failure *codedFixtureError) Code() string { return failure.code }

func fault(spec failureSpec) error {
	if spec.Kind == "nil" {
		return nil
	}
	var cause error
	if spec.Cause != nil {
		cause = fault(*spec.Cause)
	}
	if spec.Kind == "api" || spec.Kind == "syntax" {
		failure := &provider.SDKStreamError{Class: "APIError", Message: spec.Message, Payload: spec.Payload, Cause: cause}
		if spec.SDKClass != nil {
			failure.Class = *spec.SDKClass
		}
		if spec.Code != nil {
			panic("SDK property-code fixture profile is not implemented")
		}
		if spec.Kind == "syntax" {
			failure.Class, failure.Kind, failure.Payload = "SyntaxError", "syntax", nil
		}
		if spec.SelfCause {
			failure.Cause = failure
		}
		return failure
	}
	if spec.Kind != "native" {
		panic("unknown fault fixture profile")
	}
	failure := &fixtureError{message: spec.Message, cause: cause}
	if spec.Code != nil {
		coded := &codedFixtureError{fixtureError: failure, code: *spec.Code}
		if spec.SelfCause {
			failure.cause = coded
		}
		return coded
	}
	if spec.SelfCause {
		failure.cause = failure
	}
	return failure
}

func observedFault(spec failureSpec) any {
	failure := fault(spec)
	policy, err := retrypolicy.HasPolicyRejection(failure)
	if err != nil {
		panic(err)
	}
	api, err := retrypolicy.IsRetryableStreamAPIError(failure)
	if err != nil {
		panic(err)
	}
	transport, err := retrypolicy.IsRetryableStreamTransportError(failure)
	if err != nil {
		panic(err)
	}
	var code any
	if value, ok := retrypolicy.ConnectionCode(failure); ok {
		code = value
	}
	states := []bool{}
	for flags := 0; flags < 8; flags++ {
		retry, err := retrypolicy.ShouldRetryTransportDisconnect(failure, retrypolicy.DisconnectState{HasCrossedSideEffectBoundary: flags&1 != 0, StreamIdleAborted: flags&2 != 0, SignalAborted: flags&4 != 0})
		if err != nil {
			panic(err)
		}
		states = append(states, retry)
	}
	return map[string]any{"policy": policy, "api": api, "transport": transport, "code": code, "states": states}
}

func numeric(raw json.RawMessage) float64 {
	var value float64
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		panic("fixture requires explicit numeric inputs")
	}
	if text == "NaN" {
		return math.NaN()
	}
	if text == "Infinity" {
		return math.Inf(1)
	}
	if text == "-Infinity" {
		return math.Inf(-1)
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		panic(err)
	}
	return value
}

func observed(value float64) any {
	var output any = value
	if value == 0 {
		output = 0 // JSON.stringify uses +0; the separate marker retains its sign.
	} else if math.IsNaN(value) {
		output = "NaN"
	} else if math.IsInf(value, 1) {
		output = "Infinity"
	} else if math.IsInf(value, -1) {
		output = "-Infinity"
	}
	return map[string]any{"value": output, "negativeZero": value == 0 && math.Signbit(value)}
}

func main() {
	var input struct {
		Home        string        `json:"home"`
		Environment *string       `json:"environment"`
		Delays      []step        `json:"delays"`
		Failures    []failureSpec `json:"failures"`
	}
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		panic(err)
	}
	cwd, err := os.Getwd()
	root := os.Getenv("CC_HAHA_FIXTURE_TEMP_ROOT")
	canonicalHome, homeErr := filepath.EvalSymlinks(input.Home)
	canonicalRoot, rootErr := filepath.EvalSymlinks(root)
	relative, relativeErr := filepath.Rel(canonicalRoot, canonicalHome)
	if err != nil || homeErr != nil || rootErr != nil || relativeErr != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) || root == "" || canonicalHome != cwd || input.Home != os.Getenv("HOME") || input.Home != os.Getenv("USERPROFILE") || filepath.Join(input.Home, ".claude") != os.Getenv("CLAUDE_CONFIG_DIR") || os.Getenv("CC_HAHA_RETRY_DELAY_FIXTURE") != "1" || os.Getenv("BUN_OPTIONS") != "--no-env-file" {
		panic("retry fixture requires disposable HOME/config before business execution")
	}
	for _, path := range []string{input.Home, filepath.Join(input.Home, ".claude")} {
		stat, err := os.Lstat(path)
		if os.IsNotExist(err) && path != input.Home {
			continue
		}
		if err != nil || !stat.IsDir() || stat.Mode()&os.ModeSymlink != 0 {
			panic("retry fixture requires nonsymlink private directories")
		}
	}
	if input.Failures != nil {
		outcomes := []any{}
		for _, failure := range input.Failures {
			outcomes = append(outcomes, observedFault(failure))
		}
		raw, err := json.Marshal(map[string]any{"outcomes": outcomes})
		if err != nil {
			panic(err)
		}
		fmt.Print(string(raw))
		return
	}
	outputs := []any{}
	for _, entry := range input.Delays {
		calls := 0
		value := retrypolicy.Delay(numeric(entry.Attempt), entry.Header, numeric(entry.Maximum), func() float64 { calls++; return numeric(entry.Random) })
		outputs = append(outputs, map[string]any{"result": observed(value), "randomCalls": calls})
	}
	env := ""
	if input.Environment != nil {
		env = *input.Environment
	}
	result := map[string]any{"outputs": outputs, "maximum": observed(retrypolicy.DefaultMaximum(env)), "streamMaximum": observed(retrypolicy.StreamTransientMaximum(env))}
	raw, err := json.Marshal(result)
	if err != nil {
		panic(err)
	}
	fmt.Print(string(raw))
}
