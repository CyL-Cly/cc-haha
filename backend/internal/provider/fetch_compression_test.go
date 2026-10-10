package provider

import (
	"io"
	"strings"
	"testing"
)

func TestMalformedCompressedBodyClosesItsRealPipeAfterReadFailure(t *testing.T) {
	for _, encoding := range []string{"gzip", "deflate", "br", "zstd"} {
		t.Run(encoding, func(t *testing.T) {
			payload := "THIS_IS_NOT_A_COMPRESSED_STREAM"
			wire := "HTTP/1.1 200 OK\r\nContent-Encoding: " + encoding + "\r\nContent-Length: 31\r\n\r\n" + payload
			response, err := pipeExchange(wire, nil).Do(exchangeRequest(t))
			if err != nil {
				t.Fatal(err)
			}
			_, err = io.ReadAll(response.Response.Body)
			if err == nil {
				t.Fatal("malformed compressed body accepted")
			}
			if err = response.Response.Body.Close(); err != nil && !strings.Contains(err.Error(), "closed") {
				t.Fatal(err)
			}
			_ = response.Response.Body.Close()
		})
	}
}

func TestNativeReadErrorProjectionDoesNotReclassifyAdministrativeIOCause(t *testing.T) {
	for _, cause := range []error{io.ErrUnexpectedEOF, io.ErrClosedPipe} {
		failure := &ownerWaitFailure{cause}
		if projected := nativeBodyReadError(failure); projected != failure || !IsOwnerWaitFailure(projected) {
			t.Fatal("administrative failure changed", projected)
		}
	}
}
