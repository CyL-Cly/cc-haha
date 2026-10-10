package mcpruntime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/imageprocessing"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpclient"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/webfetch"
)

// RichResultOptions is an immutable owner-produced snapshot. OnSettled retains
// the native lifetime until all original Promise.all siblings have finished,
// even when one rejected result has already been delivered to the query.
type RichResultOptions struct {
	Directory        string
	ImageLibraryPath string
	Codec            imageprocessing.Codec
	NowMillis        func() float64
	Random           func() float64
	MkdirAll         func(string) error
	WriteFile        func(string, []byte) error
	OnSettled        func()
}
type richResultJob struct {
	block        json.RawMessage
	persistID    string
	data         []byte
	prepareError error
}
type richResultCompletion struct {
	index  int
	blocks []protocol.JSONObject
	err    error
}

func TransformRichResult(ctx context.Context, result protocol.JSONObject, tool, server string, o RichResultOptions) (json.RawMessage, error) {
	settle := func() {
		if o.OnSettled != nil {
			o.OnSettled()
		}
	}
	if result.Get("toolResult") != nil {
		defer settle()
		return TransformResult(result, tool, server)
	}
	source := bytesTrim(result.Get("content"))
	if len(source) == 0 || source[0] != '[' {
		defer settle()
		return TransformResult(result, tool, server)
	}
	var blocks []json.RawMessage
	if e := json.Unmarshal(source, &blocks); e != nil {
		settle()
		return nil, e
	}
	if o.NowMillis == nil {
		o.NowMillis = func() float64 { return float64(time.Now().UnixMilli()) }
	}
	if o.Random == nil {
		o.Random = rand.Float64
	}
	if o.MkdirAll == nil {
		o.MkdirAll = func(path string) error { return os.MkdirAll(path, 0777) }
	}
	if o.WriteFile == nil {
		o.WriteFile = func(path string, data []byte) error { return os.WriteFile(path, data, 0666) }
	}
	jobs := make([]richResultJob, len(blocks))
	// Each async Source call reaches its first await during Array.map. Blob IDs
	// are allocated there, before any peer can reject or finish its filesystem IO.
	for i, p := range blocks {
		jobs[i].block = p
		b := resultObject(p)
		resource := resultObject(b.Get("resource"))
		if b.String("type") == "audio" || b.String("type") == "resource" && resource.Get("text") == nil && resource.Get("blob") != nil && !richImageMIME(resource.String("mimeType")) {
			value := b.Get("data")
			if b.String("type") == "resource" {
				value = resource.Get("blob")
			}
			jobs[i].data, jobs[i].prepareError = richNodeBuffer(value)
			if jobs[i].prepareError != nil {
				continue
			}
			stamp, _ := protocol.ExactJavaScriptStringJSON(resultRaw(o.NowMillis()))
			var timestamp string
			json.Unmarshal(stamp, &timestamp)
			jobs[i].persistID = "mcp-" + mcpclient.NormalizeName(server) + "-blob-" + timestamp + "-" + webfetch.RandomBase36Suffix(o.Random())
		}
	}
	done := make(chan richResultCompletion, len(jobs))
	var wg sync.WaitGroup
	for i, job := range jobs {
		wg.Add(1)
		go func(index int, job richResultJob) {
			defer wg.Done()
			p, e := transformRichBlock(job, server, o)
			done <- richResultCompletion{index, p, e}
		}(i, job)
	}
	go func() { wg.Wait(); settle() }()
	content := make([][]protocol.JSONObject, len(jobs))
	for range jobs {
		select {
		case value := <-done:
			if value.err != nil {
				return nil, value.err
			}
			content[value.index] = value.blocks
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	flat := []protocol.JSONObject{}
	for _, part := range content {
		flat = append(flat, part...)
	}
	if structured := result.Get("structuredContent"); structured != nil {
		serialized := protocol.StringifyValidatedJavaScriptJSON(structured)
		present := false
		for _, block := range flat {
			if block.String("type") != "text" {
				continue
			}
			units, e := protocol.DecodeJavaScriptJSONString(block.Get("text"))
			if e != nil {
				continue
			}
			for len(units) > 0 && resultWhitespace(units[0]) {
				units = units[1:]
			}
			for len(units) > 0 && resultWhitespace(units[len(units)-1]) {
				units = units[:len(units)-1]
			}
			trimmed := protocol.JavaScriptJSONSourceUTF16(units)
			if trimmed == string(serialized) || json.Valid([]byte(trimmed)) && jsonEqual([]byte(trimmed), structured) {
				present = true
				break
			}
		}
		if !present {
			flat = append(flat, textBlock(resultRaw("Structured content:\n"+string(serialized))))
		}
	}
	return resultRaw(flat), nil
}
func richImageMIME(value string) bool {
	return value == "image/jpeg" || value == "image/png" || value == "image/gif" || value == "image/webp"
}
func richString(value json.RawMessage) json.RawMessage {
	p, _ := protocol.ExactJavaScriptStringJSON(value)
	return p
}
func richPrefix(server string, uri json.RawMessage) json.RawMessage {
	return appendText(resultRaw("[Resource from "+server+" at "), richString(uri), resultRaw("] "))
}
func transformRichBlock(job richResultJob, server string, o RichResultOptions) ([]protocol.JSONObject, error) {
	p := bytesTrim(job.block)
	if string(p) == "null" {
		return nil, fmt.Errorf("null is not an object (evaluating 'resultContent.type')")
	}
	b := resultObject(p)
	if job.prepareError != nil {
		return nil, job.prepareError
	}
	switch b.String("type") {
	case "text":
		return []protocol.JSONObject{textBlock(b.Get("text"))}, nil
	case "resource_link":
		text := appendText(resultRaw("[Resource link: "), richString(b.Get("name")), resultRaw("] "), richString(b.Get("uri")))
		if resultTruthy(b.Get("description")) {
			text = appendText(text, resultRaw(" ("), richString(b.Get("description")), resultRaw(")"))
		}
		return []protocol.JSONObject{textBlock(text)}, nil
	case "image":
		data, e := richNodeBuffer(richString(b.Get("data")))
		if e != nil {
			return nil, e
		}
		image, e := richImage(data, b.Get("mimeType"), o)
		if e != nil {
			return nil, e
		}
		return []protocol.JSONObject{image}, nil
	case "audio":
		return richPersist(job.data, b.Get("mimeType"), appendText(resultRaw("[Audio from "+server+"] ")), job.persistID, o)
	case "resource":
		resource := resultObject(b.Get("resource"))
		if !resource.IsObject() {
			return nil, fmt.Errorf("null is not an object (evaluating 'resource.uri')")
		}
		prefix := richPrefix(server, resource.Get("uri"))
		if resource.Get("text") != nil {
			return []protocol.JSONObject{textBlock(appendText(prefix, richString(resource.Get("text"))))}, nil
		}
		if blob := resource.Get("blob"); blob != nil {
			data := job.data
			if richImageMIME(resource.String("mimeType")) {
				var e error
				data, e = richNodeBuffer(blob)
				if e != nil {
					return nil, e
				}
				image, e := richImage(data, resource.Get("mimeType"), o)
				if e != nil {
					return nil, e
				}
				return []protocol.JSONObject{textBlock(prefix), image}, nil
			}
			return richPersist(data, resource.Get("mimeType"), prefix, job.persistID, o)
		}
	}
	return []protocol.JSONObject{}, nil
}
func resultTruthy(value json.RawMessage) bool {
	p := bytesTrim(value)
	if len(p) == 0 || string(p) == "null" || string(p) == "false" || string(p) == `""` {
		return false
	}
	if p[0] == '-' || p[0] >= '0' && p[0] <= '9' {
		n, _ := strconv.ParseFloat(string(p), 64)
		return n != 0
	}
	return true
}
func richImage(data []byte, mime json.RawMessage, o RichResultOptions) (protocol.JSONObject, error) {
	extension := "png"
	if len(mime) > 0 && string(bytesTrim(mime)) != "null" {
		var m string
		if json.Unmarshal(mime, &m) != nil {
			return protocol.JSONObject{}, fmt.Errorf("resultContent.mimeType?.split is not a function. (In 'resultContent.mimeType?.split(\"/\")', 'resultContent.mimeType?.split' is undefined)")
		}
		parts := strings.Split(m, "/")
		if len(parts) > 1 && parts[1] != "" {
			extension = parts[1]
		}
	}
	codec := o.Codec
	if codec == nil {
		codec, _ = imageprocessing.BundledCodec(o.ImageLibraryPath)
	}
	resized, e := imageprocessing.Normalize(codec, data, len(data), extension)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	source := protocol.NewJSONObject(protocol.JSONField{Name: "data", Value: resultRaw(base64.StdEncoding.EncodeToString(resized.Buffer))}, protocol.JSONField{Name: "media_type", Value: resultRaw("image/" + resized.MediaType)}, protocol.JSONField{Name: "type", Value: resultRaw("base64")})
	return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: resultRaw("image")}, protocol.JSONField{Name: "source", Value: resultRaw(source)}), nil
}
func richPersist(data []byte, mime, prefix json.RawMessage, id string, o RichResultOptions) ([]protocol.JSONObject, error) {
	if !filepath.IsAbs(o.Directory) {
		return nil, &ResultCapabilityError{Boundary: "owner-bound tool-results directory"}
	}
	_ = o.MkdirAll(o.Directory)
	mt := ""
	if resultTruthy(mime) {
		if json.Unmarshal(mime, &mt) != nil {
			return nil, fmt.Errorf("mimeType.split is not a function. (In 'mimeType.split(\";\")', 'mimeType.split' is undefined)")
		}
	}
	path := filepath.Join(o.Directory, id+"."+webfetch.ExtensionForMIME(mt))
	failure := o.WriteFile(path, data)
	mtText := mime
	if !resultTruthy(mtText) {
		mtText = resultRaw("unknown type")
	}
	if failure != nil {
		return []protocol.JSONObject{textBlock(appendText(prefix, resultRaw("Binary content ("), richString(mtText), resultRaw(fmt.Sprintf(", %d bytes) could not be saved to disk: ", len(data))), resultRaw(strings.TrimPrefix(storage.JavaScriptErrorText(failure), "Error: "))))}, nil
	}
	return []protocol.JSONObject{textBlock(appendText(prefix, resultRaw("Binary content ("), richString(mtText), resultRaw(", "+webfetch.FormatSize(len(data))+") saved to "+path)))}, nil
}
func richNodeBuffer(value json.RawMessage) ([]byte, error) {
	p := bytesTrim(value)
	if len(p) > 0 && p[0] == '"' {
		units, e := protocol.DecodeJavaScriptJSONString(p)
		if e != nil {
			return nil, e
		}
		var clean strings.Builder
		for _, u := range units {
			if u == '=' {
				break
			}
			if u == '-' {
				u = '+'
			}
			if u == '_' {
				u = '/'
			}
			if u >= 'A' && u <= 'Z' || u >= 'a' && u <= 'z' || u >= '0' && u <= '9' || u == '+' || u == '/' {
				clean.WriteByte(byte(u))
			}
		}
		text := clean.String()
		if len(text)%4 == 1 {
			text = text[:len(text)-1]
		}
		data, _ := base64.RawStdEncoding.DecodeString(text)
		return data, nil
	}
	var values []json.RawMessage
	if len(p) > 0 && p[0] == '[' {
		json.Unmarshal(p, &values)
	} else if len(p) > 0 && p[0] == '{' {
		o := resultObject(p)
		if o.String("type") == "Buffer" {
			json.Unmarshal(o.Get("data"), &values)
		}
	}
	if values != nil {
		data := make([]byte, len(values))
		for i, v := range values {
			n := richBufferNumber(v)
			if math.IsNaN(n) || math.IsInf(n, 0) {
				n = 0
			}
			data[i] = byte(int64(math.Mod(math.Trunc(n), 256)) & 255)
		}
		return data, nil
	}
	received := string(p)
	if len(p) == 0 {
		received = "undefined"
	} else if p[0] == '-' || p[0] >= '0' && p[0] <= '9' {
		received = "type number (" + string(p) + ")"
	} else if string(p) == "true" || string(p) == "false" {
		received = "type boolean (" + string(p) + ")"
	} else if p[0] == '{' {
		received = "an instance of Object"
	}
	return nil, fmt.Errorf("The first argument must be of type string, Buffer, ArrayBuffer, Array, or Array-like Object. Received %s", received)
}

func richBufferNumber(p json.RawMessage) float64 {
	p = bytesTrim(p)
	if string(p) == "null" {
		return 0
	}
	if string(p) == "true" {
		return 1
	}
	if string(p) == "false" {
		return 0
	}
	if len(p) > 0 && (p[0] == '"' || p[0] == '[') {
		q, e := protocol.ExactJavaScriptStringJSON(p)
		if e != nil {
			return math.NaN()
		}
		var text string
		json.Unmarshal(q, &text)
		text = protocol.TrimJavaScriptString(text)
		if text == "" {
			return 0
		}
		if len(text) > 2 && text[0] == '0' {
			base := 0
			switch text[1] {
			case 'x', 'X':
				base = 16
			case 'b', 'B':
				base = 2
			case 'o', 'O':
				base = 8
			}
			if base != 0 {
				n, e := strconv.ParseUint(text[2:], base, 64)
				if e == nil {
					return float64(n)
				}
				return math.NaN()
			}
		}
		n, e := strconv.ParseFloat(text, 64)
		if e != nil && !math.IsInf(n, 0) {
			return math.NaN()
		}
		return n
	}
	n, e := strconv.ParseFloat(string(p), 64)
	if e != nil && !math.IsInf(n, 0) {
		return math.NaN()
	}
	return n
}
