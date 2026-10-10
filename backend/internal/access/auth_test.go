package access

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"
)

func authorizationHeaders(value string) http.Header {
	return http.Header{"Authorization": []string{value}}
}

func stringPointer(value string) *string { return &value }

func TestBearerParsingPreservesReferenceSyntax(t *testing.T) {
	for _, fixture := range []struct {
		header, token, message string
	}{
		{"", "", "Missing Authorization header"},
		{"Basic token", "", "Invalid Authorization format. Use: Bearer <token>"},
		{"bearer token", "", "Invalid Authorization format. Use: Bearer <token>"},
		{"Bearer\ttoken", "", "Invalid Authorization format. Use: Bearer <token>"},
		{"Bearer  token", "", "Invalid Authorization format. Use: Bearer <token>"},
		{"Bearer token extra", "token", ""},
		{"Bearer token", "token", ""},
	} {
		result, token := parseBearerToken(fixture.header)
		if result.Valid != (fixture.message == "") || result.Error != fixture.message || token != fixture.token {
			t.Fatalf("header=%q result=%+v token=%q", fixture.header, result, token)
		}
	}
}

func TestIndependentLocalAndPetCredentials(t *testing.T) {
	auth := Authorization{LocalToken: "\ufeff desktop中文 \u2028", PetToken: "pet", APIKey: "api"}
	if !auth.HasLocalToken() || !auth.HasPetToken() || !auth.LocalAuthorized(authorizationHeaders("Bearer desktop中文"), nil) || auth.PetAuthorized(authorizationHeaders("Bearer desktop中文"), nil) {
		t.Fatal("credentials crossed or configured whitespace was retained")
	}
	if !auth.PetAuthorized(authorizationHeaders("Bearer pet"), nil) || auth.LocalAuthorized(authorizationHeaders("Bearer pet"), nil) || auth.LocalAuthorized(nil, stringPointer("wrong")) || auth.LocalAuthorized(nil, stringPointer("")) || !auth.LocalAuthorized(nil, stringPointer("desktop中文")) {
		t.Fatal("token override precedence changed")
	}
	for _, blank := range []string{"", " \t\r\n", "\ufeff"} {
		unset := Authorization{LocalToken: blank, PetToken: blank}
		if unset.HasLocalToken() || unset.HasPetToken() || unset.LocalAuthorized(nil, stringPointer("anything")) || unset.PetAuthorized(nil, stringPointer("anything")) {
			t.Fatalf("blank token %q enabled access", blank)
		}
	}
	if !(Authorization{LocalToken: "\u0085"}).HasLocalToken() {
		t.Fatal("NEL is not JavaScript trim whitespace")
	}
}

func TestAPIKeyValidation(t *testing.T) {
	for _, fixture := range []struct {
		key, header, message string
	}{
		{"api", "", "Missing Authorization header"},
		{"api", "Basic api", "Invalid Authorization format. Use: Bearer <token>"},
		{"", "Bearer api", "Server ANTHROPIC_API_KEY not configured"},
		{"api", "Bearer wrong", "Invalid API key"},
		{"api", "Bearer api", ""},
	} {
		result := (Authorization{APIKey: fixture.key}).ValidateAPIKey(authorizationHeaders(fixture.header))
		if result.Error != fixture.message || result.Valid != (fixture.message == "") {
			t.Fatalf("fixture=%+v result=%+v", fixture, result)
		}
	}
}

func TestRequestAuthenticationAndRejections(t *testing.T) {
	var tokens []string
	auth := Authorization{APIKey: "api", LocalToken: "desktop", PetToken: "pet", ValidateH5Token: func(_ context.Context, token string) (bool, error) {
		tokens = append(tokens, token)
		return token == "phone", nil
	}}
	for _, fixture := range []struct {
		header, message string
		override        *string
	}{
		{header: "Bearer desktop"},
		{header: "Bearer api", override: stringPointer("wrong")},
		{header: "Bearer phone"},
		{override: stringPointer("phone")},
		{header: "Bearer wrong", message: "Invalid H5 access token"},
		{header: "Bearer phone", override: stringPointer(""), message: "Invalid API key"},
		{header: "Basic phone", message: "Invalid Authorization format. Use: Bearer <token>"},
	} {
		result, err := auth.ValidateRequest(context.Background(), authorizationHeaders(fixture.header), fixture.override)
		if err != nil || result.Valid != (fixture.message == "") || result.Error != fixture.message {
			t.Fatalf("fixture=%+v result=%+v err=%v", fixture, result, err)
		}
		rejection := RejectUnauthorized(result)
		if result.Valid && rejection != nil || !result.Valid && (rejection.Status != 401 || rejection.Body["error"] != "Unauthorized" || rejection.Body["message"] != fixture.message) {
			t.Fatal("rejection changed")
		}
	}
	if !reflect.DeepEqual(tokens, []string{"phone", "phone", "wrong"}) {
		t.Fatalf("unnecessary H5 state reads: %v", tokens)
	}
	for _, fixture := range []struct {
		header, message string
		override        *string
	}{
		{message: "Missing H5 access token"},
		{header: "Bearer api", message: "Invalid H5 access token"},
		{header: "Bearer phone"},
		{header: "Basic bad", override: stringPointer("phone")},
		{header: "Bearer phone", override: stringPointer(""), message: "Missing H5 access token"},
	} {
		result, err := auth.RequireH5(context.Background(), authorizationHeaders(fixture.header), fixture.override)
		if err != nil || result.Valid != (fixture.message == "") || result.Error != fixture.message {
			t.Fatalf("H5 fixture=%+v result=%+v err=%v", fixture, result, err)
		}
	}
}

func TestH5ValidatorFailuresPropagate(t *testing.T) {
	for _, auth := range []Authorization{
		{},
		{ValidateH5Token: func(context.Context, string) (bool, error) { return false, errors.New("settings unavailable") }},
	} {
		for _, run := range []func() (AuthResult, error){
			func() (AuthResult, error) {
				return auth.ValidateRequest(context.Background(), nil, stringPointer("phone"))
			},
			func() (AuthResult, error) { return auth.RequireH5(context.Background(), nil, stringPointer("phone")) },
		} {
			result, err := run()
			if result.Valid || err == nil {
				t.Fatal("validator failure became authentication success")
			}
		}
	}
}
