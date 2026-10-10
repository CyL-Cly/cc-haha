package provider

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
)

// These are physical fault facts, not SDK/Bun errors or owner policy.
type ownerNativeFault struct {
	operation string
	cause     error
}

func (err *ownerNativeFault) Error() string { return err.cause.Error() }

type ownerPhysicalProfile struct{ boundary string }

func (err *ownerPhysicalProfile) Error() string { return err.boundary }

func physicalOpen(wait context.Context, lease *ownerLease, plan *SendPlan) ([]byte, error) {
	if !lease.begin() {
		return nil, context.Canceled
	}
	defer lease.work.Done()
	stop := context.AfterFunc(wait, lease.closePhysical)
	defer stop() // successful Await's later cancel must not cancel the body lease
	request, err := http.NewRequestWithContext(lease.ctx, plan.method, plan.endpoint, bytes.NewReader(plan.body))
	if err != nil {
		return nil, &ownerNativeFault{"request", err}
	}
	request.Header = plan.headers.Clone()
	address := request.URL.Host
	if request.URL.Port() == "" {
		port := "80"
		if plan.trust != nil {
			port = "443"
		}
		address = net.JoinHostPort(request.URL.Hostname(), port)
	}
	connection, err := (&net.Dialer{}).DialContext(lease.ctx, "tcp", address)
	if err != nil {
		return nil, &ownerNativeFault{"dial", err}
	}
	if !lease.setConnection(connection) {
		return nil, context.Canceled
	}
	if plan.trust != nil {
		secured := tls.Client(connection, plan.trust)
		if !lease.setConnection(secured) {
			return nil, context.Canceled
		}
		if err := secured.HandshakeContext(lease.ctx); err != nil {
			return nil, &ownerNativeFault{"handshake", err}
		}
		connection = secured
	}
	if err := request.Write(connection); err != nil {
		return nil, &ownerNativeFault{"write", err}
	}
	reader := bufio.NewReaderSize(connection, plan.headerLimit+1)
	var block []byte
	for length := 1; length <= plan.headerLimit; length++ {
		peek, err := reader.Peek(length)
		if err != nil {
			return nil, &ownerNativeFault{"headers", err}
		}
		if length >= 4 && bytes.Equal(peek[length-4:], []byte("\r\n\r\n")) {
			block = append([]byte(nil), peek...)
			break
		}
	}
	if block == nil {
		return nil, &ownerPhysicalProfile{"response headers exceed capture bound"}
	}
	response, err := http.ReadResponse(reader, request)
	if err != nil {
		return nil, &ownerNativeFault{"response", err}
	}
	lease.mu.Lock()
	lease.body = nativeFetchBody(response.Body, response.Header.Get("Content-Encoding"), request.URL.String())
	if lease.body != response.Body {
		lease.decoder = lease.body
	}
	lease.mu.Unlock()
	if lease.timer != nil {
		lease.timer.Stop()
	} // resource timer, not owner callback
	return json.Marshal(ownerHead{Status: response.StatusCode, Headers: block})
}
