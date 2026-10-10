package cloudauth

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type HTTPDo func(*http.Request) (*http.Response, error)

// STS accepts a physical transport callback, so the CLI can bind its native
// owner/proxy/CA transport and tests can bind an actual private HTTP server.
type STS struct {
	Region, Endpoint string
	Do               HTTPDo
	Now              func() time.Time
}

func (s STS) endpoint() string {
	if s.Endpoint != "" {
		return s.Endpoint
	}
	suffix := "amazonaws.com"
	if strings.HasPrefix(s.Region, "cn-") {
		suffix += ".cn"
	}
	return "https://sts." + s.Region + "." + suffix + "/"
}

func (s STS) query(ctx context.Context, action string, params [][2]string, credentials *AWSCredentials) ([]byte, error) {
	if s.Do == nil || s.Region == "" {
		return nil, errors.New("STS requires explicit region and native HTTP transport")
	}
	fields := [][2]string{{"Action", action}, {"Version", "2011-06-15"}}
	fields = append(fields, params...)
	parts := make([]string, len(fields))
	for i, field := range fields {
		parts[i] = escapeURI(field[0]) + "=" + escapeURI(field[1])
	}
	body := strings.Join(parts, "&")
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint(), strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if credentials != nil {
		now := time.Now()
		if s.Now != nil {
			now = s.Now()
		}
		signed := Sign(SigningRequest{Method: request.Method, Path: request.URL.EscapedPath(), Query: request.URL.Query(), Headers: map[string]string{"host": request.URL.Host, "content-type": request.Header.Get("Content-Type")}, Body: []byte(body)}, *credentials, SignOptions{Region: s.Region, Service: "sts", Time: now})
		for key, value := range signed.Headers {
			if key == "host" {
				request.Host = value
			} else {
				request.Header.Set(key, value)
			}
		}
	}
	response, err := s.Do(request)
	if err != nil {
		return nil, err
	}
	if response == nil || response.Body == nil {
		return nil, errors.New("STS transport returned no response body")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 2*1024*1024 {
		return nil, errors.New("STS response exceeds bounded credential envelope")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var failure struct {
			Error struct{ Code, Message string }
		}
		_ = xml.Unmarshal(data, &failure)
		return nil, fmt.Errorf("STS %s HTTP %d: %s: %s", action, response.StatusCode, failure.Error.Code, failure.Error.Message)
	}
	return data, nil
}

func (s STS) CallerIdentity(ctx context.Context, credentials AWSCredentials) error {
	_, err := s.query(ctx, "GetCallerIdentity", nil, &credentials)
	return err
}

func (s STS) AssumeRole(ctx context.Context, credentials AWSCredentials, params [][2]string) (AWSCredentials, error) {
	raw, err := s.query(ctx, "AssumeRole", params, &credentials)
	if err != nil {
		return AWSCredentials{}, err
	}
	return decodeSTSCredentials(raw)
}
func (s STS) AssumeRoleWithWebIdentity(ctx context.Context, params [][2]string) (AWSCredentials, error) {
	raw, err := s.query(ctx, "AssumeRoleWithWebIdentity", params, nil)
	if err != nil {
		return AWSCredentials{}, err
	}
	return decodeSTSCredentials(raw)
}

func decodeSTSCredentials(raw []byte) (AWSCredentials, error) {
	decoder := xml.NewDecoder(strings.NewReader(string(raw)))
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return AWSCredentials{}, errors.New("Invalid response from STS")
		}
		if err != nil {
			return AWSCredentials{}, err
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "Credentials" {
			continue
		}
		var credentials struct {
			AccessKeyID                               string `xml:"AccessKeyId"`
			SecretAccessKey, SessionToken, Expiration string
		}
		if err = decoder.DecodeElement(&credentials, &start); err != nil {
			return AWSCredentials{}, err
		}
		if credentials.AccessKeyID == "" || credentials.SecretAccessKey == "" || credentials.SessionToken == "" {
			return AWSCredentials{}, errors.New("Invalid response from STS")
		}
		value := AWSCredentials{AccessKeyID: credentials.AccessKeyID, SecretAccessKey: credentials.SecretAccessKey, SessionToken: &credentials.SessionToken}
		if credentials.Expiration != "" {
			stamp, err := time.Parse(time.RFC3339Nano, credentials.Expiration)
			if err != nil {
				return AWSCredentials{}, err
			}
			value.Expiration = &stamp
		}
		return value, nil
	}
}

// ParseEndpoint validates an explicit source endpoint override before any I/O.
func ParseEndpoint(value string) (*url.URL, error) {
	u, err := url.Parse(value)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "https" && u.Scheme != "http" || u.Host == "" || u.User != nil {
		return nil, errors.New("invalid cloud endpoint")
	}
	return u, nil
}
