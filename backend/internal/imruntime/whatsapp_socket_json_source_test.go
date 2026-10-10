package imruntime

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestWhatsAppActualSocketProtobufMediaNamesAgainstNodeDecoder(t *testing.T) {
	image := &waE2E.ImageMessage{URL: proto.String("https://mmg.whatsapp.net/private"), DirectPath: proto.String("/private"), Mimetype: proto.String("image/png"), MediaKey: []byte{1, 2, 3}, FileSHA256: []byte{4, 5}, FileEncSHA256: []byte{6, 7}, JPEGThumbnail: []byte{8, 9}, Width: proto.Uint32(7), Height: proto.Uint32(5), FileLength: proto.Uint64(123), MediaKeyTimestamp: proto.Int64(456)}
	for _, message := range []*waE2E.Message{{ImageMessage: image}, {ViewOnceMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{ImageMessage: image}}}, {AudioMessage: &waE2E.AudioMessage{URL: image.URL, MediaKey: image.MediaKey, FileSHA256: image.FileSHA256, FileEncSHA256: image.FileEncSHA256, PTT: proto.Bool(true), Seconds: proto.Uint32(3)}}, {DocumentMessage: &waE2E.DocumentMessage{URL: image.URL, FileName: proto.String("private.pdf"), JPEGThumbnail: image.JPEGThumbnail, ThumbnailSHA256: image.FileSHA256, ThumbnailEncSHA256: image.FileEncSHA256}}} {
		wire, e := proto.Marshal(message)
		if e != nil {
			t.Fatal(e)
		}
		source := whatsAppOriginal(t, "native-whatsapp-sdk-media.fixture.ts", map[string]any{"op": "decodeMessage", "data": base64.StdEncoding.EncodeToString(wire)})
		encoded, e := protojson.Marshal(message)
		if e != nil {
			t.Fatal(e)
		}
		var want, got any
		json.Unmarshal(source, &want)
		json.Unmarshal(whatsAppSocketJSON(encoded), &got)
		if !bytes.Equal(raw(want), raw(got)) {
			t.Fatalf("actual socket JSON Go %s Source %s", raw(got), raw(want))
		}
	}
}

func TestWhatsAppRetryContentPriorityAndWrappersActualSource(t *testing.T) {
	image := map[string]any{"mediaKey": "image-key", "url": "https://mmg.whatsapp.net/old-image"}
	document := map[string]any{"mediaKey": "document-key", "url": "https://mmg.whatsapp.net/old-document"}
	for _, content := range []any{map[string]any{"imageMessage": image}, map[string]any{"imageMessage": image, "documentMessage": document}, map[string]any{"viewOnceMessage": map[string]any{"message": map[string]any{"imageMessage": image}}, "unknown": "retained"}, map[string]any{"ephemeralMessage": map[string]any{"message": map[string]any{"documentWithCaptionMessage": map[string]any{"message": map[string]any{"documentMessage": document}}}}}, map[string]any{"buttonsMessage": map[string]any{"imageMessage": image}}, map[string]any{"buttonsMessage": map[string]any{"imageMessage": image}, "imageMessage": map[string]any{"mediaKey": "other-image", "url": "https://mmg.whatsapp.net/must-retain"}}, map[string]any{"templateMessage": map[string]any{"hydratedTemplate": map[string]any{"documentMessage": document}}}} {
		encoded := raw(content)
		source := whatsAppOriginal(t, "native-whatsapp-sdk-media.fixture.ts", map[string]any{"op": "retryContent", "message": content, "path": "/actual-reuploaded"})
		field, _, e := whatsAppRetryMedia(encoded)
		if e != nil {
			t.Fatal(e)
		}
		got := whatsAppSetRetryMediaPath(encoded, field, "/actual-reuploaded", 0)
		var gv, wv any
		json.Unmarshal(got, &gv)
		json.Unmarshal(source, &wv)
		if !bytes.Equal(raw(gv), raw(wv)) {
			t.Fatal("actual Source retry content shape", string(raw(gv)), string(raw(wv)))
		}
	}
}
