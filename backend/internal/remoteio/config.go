// Package remoteio implements print-mode RemoteIO's session-ingress transport.
// Its native I/O runs outside the model Owner; all received SDK messages still
// enter the existing StructuredIO and ASK consumers.
package remoteio

import (
	"fmt"
	"net/url"
	"strings"
)

type Kind string

const (
	WebSocket Kind = "websocket"
	Hybrid    Kind = "hybrid"
	CCR       Kind = "ccr"
)

type Endpoints struct {
	Kind                    Kind
	Stream, Events, Session string
}

func truthy(value string) bool {
	switch strings.ToLower(value) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func SelectEndpoints(raw string, env map[string]string) (Endpoints, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return Endpoints{}, fmt.Errorf("Invalid URL")
	}
	if truthy(env["CLAUDE_CODE_USE_CCR_V2"]) {
		// The original CCR constructor requires the *session* URL to be
		// http(s), even though transportUtils can convert its stream URL.
		if u.Scheme != "http" && u.Scheme != "https" {
			return Endpoints{}, fmt.Errorf("CCRClient: Expected http(s) URL, got %s:", u.Scheme)
		}
		base := *u
		base.Path = strings.TrimSuffix(base.Path, "/")
		base.RawPath, base.RawQuery, base.Fragment = "", "", ""
		stream := *u
		stream.Path = strings.TrimSuffix(stream.Path, "/") + "/worker/events/stream"
		stream.RawPath = ""
		return Endpoints{Kind: CCR, Session: base.String(), Stream: stream.String(), Events: base.String() + "/worker/events"}, nil
	}
	if u.Scheme != "ws" && u.Scheme != "wss" {
		return Endpoints{}, fmt.Errorf("Unsupported protocol: %s:", u.Scheme)
	}
	if u.Host == "" {
		return Endpoints{}, fmt.Errorf("Invalid URL")
	}
	if !truthy(env["CLAUDE_CODE_POST_FOR_SESSION_INGRESS_V2"]) {
		return Endpoints{Kind: WebSocket, Stream: u.String()}, nil
	}
	post := *u
	if post.Scheme == "wss" {
		post.Scheme = "https"
	} else {
		post.Scheme = "http"
	}
	post.Path = strings.Replace(post.Path, "/ws/", "/session/", 1)
	post.RawPath, post.Fragment = "", ""
	if !strings.HasSuffix(post.Path, "/events") {
		post.Path = strings.TrimSuffix(post.Path, "/") + "/events"
	}
	return Endpoints{Kind: Hybrid, Stream: u.String(), Events: post.String()}, nil
}

func PermanentClose(code int) bool  { return code == 1002 || code == 4001 || code == 4003 }
func PermanentHTTP(status int) bool { return status == 401 || status == 403 || status == 404 }
