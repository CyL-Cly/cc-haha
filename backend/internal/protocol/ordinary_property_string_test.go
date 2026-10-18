package protocol

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestOrdinaryPropertyStringRetainsOwnShadowingAndMissingIdentity(t *testing.T) {
	var object JSONObject
	if err := object.UnmarshalJSON([]byte(`{"constructor":"own\ud800","toString":null,"valueOf":[1,null,2],"__proto__":{"keep":true}}`)); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"constructor": `"own\ud800"`, "toString": `"null"`, "valueOf": `"1,,2"`, "__proto__": `"[object Object]"`, "unknown": "", "__defineGetter__\\ud800": ""} {
		key, _ := json.Marshal(name)
		got, err := object.GetOrdinaryPropertyStringJSON(key)
		if err != nil || string(got) != want {
			t.Errorf("%s: %s/%v want %s", name, got, err, want)
		}
	}
	if err := object.SetPropertyJSON(json.RawMessage(`"hasOwnProperty"`), nil); err != nil {
		t.Fatal(err)
	}
	if got, err := object.GetOrdinaryPropertyStringJSON(json.RawMessage(`"hasOwnProperty"`)); err != nil || got != nil {
		t.Fatal("own undefined must shadow inherited function", string(got), err)
	}
	if own, err := object.GetPropertyJSON(json.RawMessage(`"isPrototypeOf"`)); own != nil || err != nil {
		t.Fatal("ordinary coercion changed own lookup", string(own), err)
	}
}

func TestOrdinaryPropertyStringRejectsOriginalConversionErrorAndInvalidKey(t *testing.T) {
	var object JSONObject
	if err := object.UnmarshalJSON([]byte(`{"broken":{"toString":null}}`)); err != nil {
		t.Fatal(err)
	}
	_, err := object.GetOrdinaryPropertyStringJSON(json.RawMessage(`"broken"`))
	var failure *ExactJSONStringTypeError
	if !errors.As(err, &failure) || failure.Message != "No default value" {
		t.Fatal("original coercion failure lost", err)
	}
	if _, err := object.GetOrdinaryPropertyStringJSON(json.RawMessage(`7`)); err == nil {
		t.Fatal("non-string template key accepted")
	}
}
