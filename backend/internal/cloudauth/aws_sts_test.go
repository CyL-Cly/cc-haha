package cloudauth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSTSPhysicalSignedProbeRoleWebIdentityAndCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stamp := time.Date(2026, 10, 8, 1, 2, 3, 0, time.UTC)
	token := "PRIVATE_SESSION"
	credentials := AWSCredentials{AccessKeyID: "PRIVATE_ACCESS", SecretAccessKey: "PRIVATE_SECRET", SessionToken: &token}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/" {
			t.Errorf("unexpected request method/path")
		}
		params, err := url.ParseQuery(string(raw))
		if err != nil {
			t.Error(err)
		}
		if params.Get("Version") != "2011-06-15" {
			t.Error("missing STS version")
		}
		if params.Get("Action") == "AssumeRoleWithWebIdentity" {
			if r.Header.Get("Authorization") != "" || r.Header.Get("X-Amz-Security-Token") != "" {
				t.Error("web identity must be unsigned")
			}
		} else {
			signed := Sign(SigningRequest{Method: r.Method, Path: r.URL.EscapedPath(), Headers: map[string]string{"host": r.Host, "content-type": r.Header.Get("Content-Type")}, Body: raw}, credentials, SignOptions{Region: "us-east-1", Service: "sts", Time: stamp})
			if r.Header.Get("Authorization") != signed.Headers["authorization"] {
				t.Error("signature does not cover physical bytes")
			}
		}
		if params.Get("RoleArn") == "denied" {
			w.WriteHeader(403)
			_, _ = io.WriteString(w, "<ErrorResponse><Error><Code>AccessDenied</Code><Message>private denied</Message></Error></ErrorResponse>")
			return
		}
		w.Header().Set("Content-Type", "text/xml")
		if params.Get("Action") == "GetCallerIdentity" {
			_, _ = io.WriteString(w, "<GetCallerIdentityResponse><GetCallerIdentityResult><Account>123456789012</Account></GetCallerIdentityResult></GetCallerIdentityResponse>")
			return
		}
		_, _ = io.WriteString(w, "<AssumeRoleResponse><AssumeRoleResult><Credentials><AccessKeyId>NEW_PRIVATE</AccessKeyId><SecretAccessKey>NEW_SECRET</SecretAccessKey><SessionToken>NEW_TOKEN</SessionToken><Expiration>2030-01-01T00:00:00Z</Expiration></Credentials></AssumeRoleResult></AssumeRoleResponse>")
	}))
	defer server.Close()
	sts := STS{Region: "us-east-1", Endpoint: server.URL, Do: server.Client().Do, Now: func() time.Time { return stamp }}
	if err := sts.CallerIdentity(ctx, credentials); err != nil {
		t.Fatal(err)
	}
	got, err := sts.AssumeRole(ctx, credentials, [][2]string{{"RoleArn", "arn:aws:iam::123456789012:role/private"}, {"RoleSessionName", "private space"}, {"DurationSeconds", "3600"}})
	if err != nil || got.AccessKeyID != "NEW_PRIVATE" || got.Expiration == nil {
		t.Fatalf("role credential %v %v", got, err)
	}
	got, err = sts.AssumeRoleWithWebIdentity(ctx, [][2]string{{"RoleArn", "private"}, {"RoleSessionName", "private"}, {"WebIdentityToken", "private+=&token"}})
	if err != nil || got.SessionToken == nil || *got.SessionToken != "NEW_TOKEN" {
		t.Fatalf("web identity %v %v", got, err)
	}
	if _, err = sts.AssumeRole(ctx, credentials, [][2]string{{"RoleArn", "denied"}}); err == nil || !strings.Contains(err.Error(), "AccessDenied") {
		t.Fatal("STS failure swallowed", err)
	}
	stopped, stop := context.WithCancel(ctx)
	stop()
	if err = sts.CallerIdentity(stopped, credentials); !errors.Is(err, context.Canceled) {
		t.Fatal("cancel", err)
	}
	if calls.Load() != 4 {
		t.Fatal("cancel performed physical send", calls.Load())
	}
}
