// This executable is a deterministic middleware fixture, not a Server entry.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/NanmiCoder/cc-haha/backend/internal/access"
)

type fixture struct {
	APIKey, LocalToken, PetToken string
	Headers                      map[string]string
	Override                     *string
	ValidH5Tokens                []string
	Origin                       string
	H5Enabled, AllowOrigin       bool
}

func main() {
	var fixtures []fixture
	if err := json.NewDecoder(os.Stdin).Decode(&fixtures); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	results := make([]map[string]any, 0, len(fixtures))
	for _, current := range fixtures {
		headers := make(http.Header)
		for key, value := range current.Headers {
			headers.Set(key, value)
		}
		calls := make([]string, 0)
		auth := access.Authorization{APIKey: current.APIKey, LocalToken: current.LocalToken, PetToken: current.PetToken, ValidateH5Token: func(_ context.Context, token string) (bool, error) {
			calls = append(calls, token)
			for _, valid := range current.ValidH5Tokens {
				if token == valid {
					return true, nil
				}
			}
			return false, nil
		}}
		request, err := auth.ValidateRequest(context.Background(), headers, current.Override)
		if err != nil {
			panic(err)
		}
		h5, err := auth.RequireH5(context.Background(), headers, current.Override)
		if err != nil {
			panic(err)
		}
		originCalls := make([]string, 0)
		cors, err := access.ResolveCORS(context.Background(), current.Origin, access.CORSOptions{H5Enabled: current.H5Enabled, IsOriginAllowed: func(_ context.Context, origin string) (bool, error) {
			originCalls = append(originCalls, origin)
			return current.AllowOrigin, nil
		}})
		if err != nil {
			panic(err)
		}
		results = append(results, map[string]any{
			"hasLocal": auth.HasLocalToken(), "hasPet": auth.HasPetToken(),
			"local": auth.LocalAuthorized(headers, current.Override), "pet": auth.PetAuthorized(headers, current.Override),
			"apiKey": auth.ValidateAPIKey(headers), "request": request,
			"requestRejection": access.RejectUnauthorized(request), "h5": h5,
			"h5Rejection": access.RejectUnauthorized(h5), "h5Calls": calls,
			"cors": cors, "originCalls": originCalls,
		})
	}
	if err := json.NewEncoder(os.Stdout).Encode(results); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
