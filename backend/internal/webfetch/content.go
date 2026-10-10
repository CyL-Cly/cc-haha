package webfetch

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"math/big"
	"strings"
	"time"
	"unicode/utf16"
)

func IsBinaryContentType(contentType string) bool {
	if contentType == "" {
		return false
	}
	mime := strings.ToLower(trimJS(strings.Split(contentType, ";")[0]))
	if strings.HasPrefix(mime, "text/") || strings.HasSuffix(mime, "+json") || mime == "application/json" || strings.HasSuffix(mime, "+xml") || mime == "application/xml" || strings.HasPrefix(mime, "application/javascript") || mime == "application/x-www-form-urlencoded" {
		return false
	}
	return true
}
func ExtensionForMIME(contentType string) string {
	mime := strings.ToLower(trimJS(strings.Split(contentType, ";")[0]))
	extensions := map[string]string{"application/pdf": "pdf", "application/json": "json", "text/csv": "csv", "text/plain": "txt", "text/html": "html", "text/markdown": "md", "application/zip": "zip", "application/vnd.openxmlformats-officedocument.wordprocessingml.document": "docx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": "xlsx", "application/vnd.openxmlformats-officedocument.presentationml.presentation": "pptx", "application/msword": "doc", "application/vnd.ms-excel": "xls", "audio/mpeg": "mp3", "audio/wav": "wav", "audio/ogg": "ogg", "video/mp4": "mp4", "video/webm": "webm", "image/png": "png", "image/jpeg": "jpg", "image/gif": "gif", "image/webp": "webp", "image/svg+xml": "svg"}
	if ext := extensions[mime]; ext != "" {
		return ext
	}
	return "bin"
}
func FormatSize(size int) string {
	value := float64(size) / 1024
	if value < 1 {
		return fmt.Sprintf("%d bytes", size)
	}
	unit := "KB"
	if value >= 1024 {
		value /= 1024
		unit = "MB"
		if value >= 1024 {
			value /= 1024
			unit = "GB"
		}
	}
	return formatSizeFixedOne(value) + unit
}

// Number.toFixed chooses the nearest decimal integer from the exact binary64
// value, with positive ties upward. Multiplying a float first could round a
// below-tie value onto the tie (e.g. 1.15); fmt rounds exact ties to even.
func formatSizeFixedOne(value float64) string {
	rational := new(big.Rat).SetFloat64(value)
	numerator := new(big.Int).Mul(rational.Num(), big.NewInt(10))
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(numerator, rational.Denom(), remainder)
	if new(big.Int).Lsh(remainder, 1).Cmp(rational.Denom()) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	digits := quotient.String()
	if len(digits) < 2 {
		digits = "0" + digits
	}
	if digits[len(digits)-1] == '0' {
		return digits[:len(digits)-1]
	}
	return digits[:len(digits)-1] + "." + digits[len(digits)-1:]
}

func quote(value string) json.RawMessage {
	return protocol.QuoteJavaScriptUTF16(utf16.Encode([]rune(value)))
}
func SecondaryPrompt(content, prompt json.RawMessage, preapproved bool) (json.RawMessage, error) {
	guidelines := "Provide a concise response based on the content above. Include relevant details, code examples, and documentation excerpts as needed."
	if !preapproved {
		guidelines = "Provide a concise response based only on the content above. In your response:\n - Enforce a strict 125-character maximum for quotes from any source document. Open Source Software is ok as long as we respect the license.\n - Use quotation marks for exact language from articles; any language outside of the quotation should never be word-for-word the same.\n - You are not a lawyer and never comment on the legality of your own prompts and responses.\n - Never produce or reproduce exact song lyrics."
	}
	return protocol.JoinJavaScriptValuesJSON([]json.RawMessage{quote("\nWeb page content:\n---\n"), content, quote("\n---\n\n"), prompt, quote("\n\n" + guidelines + "\n")}, "")
}
func TruncateContent(content string) json.RawMessage {
	units := utf16.Encode([]rune(content))
	if len(units) > MaxMarkdownLength {
		units = append(units[:MaxMarkdownLength:MaxMarkdownLength], utf16.Encode([]rune("\n\n[Content truncated due to length...]"))...)
	}
	return protocol.QuoteJavaScriptUTF16(units)
}
func (s *Service) Call(ctx context.Context, url string, prompt json.RawMessage, nonInteractive bool) (Output, error) {
	start := s.options.Now()
	response, e := s.Fetch(ctx, url)
	if e != nil {
		return Output{}, e
	}
	return s.Process(ctx, url, prompt, nonInteractive, response, start, s.options.ApplyPrompt)
}

// Process runs the post-fetch Source body on the session owner. Fetch may wait
// on a native I/O worker; the model callback must retain its original owner.
func (s *Service) Process(ctx context.Context, url string, prompt json.RawMessage, nonInteractive bool, response Result, start time.Time, apply func(context.Context, json.RawMessage, bool) (json.RawMessage, error)) (Output, error) {
	var e error
	output := Output{URL: url}
	if response.Redirect != nil {
		redirect := response.Redirect
		statusText := "Found"
		switch redirect.StatusCode {
		case 301:
			statusText = "Moved Permanently"
		case 308:
			statusText = "Permanent Redirect"
		case 307:
			statusText = "Temporary Redirect"
		}
		message, e := protocol.JoinJavaScriptValuesJSON([]json.RawMessage{quote("REDIRECT DETECTED: The URL redirects to a different host.\n\nOriginal URL: " + redirect.OriginalURL + "\nRedirect URL: " + redirect.RedirectURL + fmt.Sprintf("\nStatus: %d %s\n\nTo complete your request, I need to fetch content from the redirected URL. Please use WebFetch again with these parameters:\n- url: \"%s\"\n- prompt: \"", redirect.StatusCode, statusText, redirect.RedirectURL)), prompt, quote("\"")}, "")
		if e != nil {
			return Output{}, e
		}
		units, _ := protocol.DecodeJavaScriptJSONString(message)
		output.Bytes = len(string(utf16.Decode(units)))
		output.Code = redirect.StatusCode
		output.CodeText = statusText
		output.Result = message
	} else {
		f := response.Fetched
		output.Bytes, output.Code, output.CodeText = f.Bytes, f.Code, f.CodeText
		preapproved := IsPreapprovedURL(url)
		if preapproved && strings.Contains(f.ContentType, "text/markdown") && len(utf16.Encode([]rune(f.Content))) < MaxMarkdownLength {
			output.Result = quote(f.Content)
		} else {
			message, e := SecondaryPrompt(TruncateContent(f.Content), prompt, preapproved)
			if e != nil {
				return Output{}, e
			}
			output.Result, e = apply(ctx, message, nonInteractive)
			if e != nil {
				return Output{}, e
			}
			if ctx.Err() != nil {
				return Output{}, ctx.Err()
			}
		}
		if f.PersistedPath != "" {
			output.Result, e = protocol.JoinJavaScriptValuesJSON([]json.RawMessage{output.Result, quote("\n\n[Binary content (" + f.ContentType + ", " + FormatSize(f.PersistedSize) + ") also saved to " + f.PersistedPath + "]")}, "")
			if e != nil {
				return Output{}, e
			}
		}
	}
	output.DurationMS = float64(s.options.Now().Sub(start).Nanoseconds()) / 1e6
	return output, nil
}
