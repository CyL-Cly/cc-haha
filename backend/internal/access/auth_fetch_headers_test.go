package access

import (
	"context"
	"net/http"
	"reflect"
	"testing"
)

// Headers are supplied in original HTTP field order. Source Fetch Headers.get
// combines them before each independent credential consumer parses the token.
func TestAuthorizationFetchHeaderConsumers(t *testing.T) {
	ctx := context.Background()
	for _, credential := range []string{"api", "desktop", "pet", "phone"} {
		t.Run(credential+"-combined", func(t *testing.T) {
			var seen []string
			auth := Authorization{APIKey: "api", LocalToken: "desktop", PetToken: "pet", ValidateH5Token: func(_ context.Context, token string) (bool, error) {
				seen = append(seen, token)
				return token == "phone", nil
			}}
			headers := make(http.Header)
			headers.Add("authorization", "Bearer "+credential)
			headers.Add("Authorization", "Bearer wrong")
			if auth.LocalAuthorized(headers, nil) || auth.PetAuthorized(headers, nil) || auth.ValidateAPIKey(headers).Valid {
				t.Fatal("Fetch-combined duplicate Authorization must not admit first credential")
			}
			result, err := auth.ValidateRequest(ctx, headers, nil)
			h5, h5err := auth.RequireH5(ctx, headers, nil)
			if err != nil || h5err != nil || result.Valid || h5.Valid || result.Error != "Invalid H5 access token" || h5.Error != "Invalid H5 access token" || !reflect.DeepEqual(seen, []string{credential + ",", credential + ","}) {
				t.Fatalf("combined token and actual H5 callback order: result=%+v h5=%+v errors=%v/%v seen=%v", result, h5, err, h5err, seen)
			}
			seen = nil
			result, err = auth.ValidateRequest(ctx, headers, stringPointer("desktop"))
			if err != nil || !result.Valid || len(seen) != 0 {
				t.Fatal("explicit local override changed or consulted H5")
			}
		})
	}
	t.Run("whitespace-normalized", func(t *testing.T) {
		auth := Authorization{APIKey: "api", LocalToken: "desktop", PetToken: "pet"}
		for _, token := range []string{"api", "desktop", "pet"} {
			headers := make(http.Header)
			headers.Add("authorization", " \tBearer "+token+"\t ")
			valid := auth.ValidateAPIKey(headers).Valid || auth.LocalAuthorized(headers, nil) || auth.PetAuthorized(headers, nil)
			if !valid {
				t.Fatal("Fetch boundary trims outer HTTP header whitespace")
			}
		}
	})
	t.Run("bearer-extra-words-preserved", func(t *testing.T) {
		headers := make(http.Header)
		headers.Add("Authorization", "Bearer api ignored")
		headers.Add("Authorization", "Bearer wrong")
		if !(Authorization{APIKey: "api"}).ValidateAPIKey(headers).Valid {
			t.Fatal("Source destructures first two bearer split words")
		}
	})
}
