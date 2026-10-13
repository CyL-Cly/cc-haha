package whatsappcandidate

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

type originalMessageWire struct {
	Name, Wire string
	Expected   json.RawMessage
}

func originalMessageWires(t *testing.T, wires []string) []originalMessageWire {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual original message protobuf producer requires Source Bun")
	}
	home := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, bun, "--no-env-file", nodeSourceFixturePath("node_message_wire.fixture.ts"))
	child.Dir = home
	child.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_WHATSAPP_LEAF=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	input := map[string]any{"homeDir": home}
	if wires != nil {
		input["wires"] = wires
	}
	child.Stdin = bytes.NewReader(nodeJSON(input))
	var stderr bytes.Buffer
	child.Stderr = &stderr
	data, err := child.Output()
	if err != nil {
		t.Fatalf("original protobuf message codec %v %.2500s", err, stderr.String())
	}
	var rows []originalMessageWire
	if err = json.Unmarshal(data, &rows); err != nil || len(rows) != 7 {
		t.Fatalf("original message inventory %s %v", data, err)
	}
	return rows
}

func TestActualOriginalMessageWireMediaEditReactionAndFutureFieldColdForwarding(t *testing.T) {
	source := originalMessageWires(t, nil)
	forwarded := []string{}
	unknown := protowire.AppendTag(nil, 500, protowire.BytesType)
	unknown = protowire.AppendBytes(unknown, []byte("opaque next-version outer field"))
	nestedUnknown := protowire.AppendTag(nil, 501, protowire.BytesType)
	nestedUnknown = protowire.AppendBytes(nestedUnknown, []byte("opaque next-version nested field"))
	for _, row := range source {
		t.Run(row.Name, func(t *testing.T) {
			wire, err := base64.StdEncoding.DecodeString(row.Wire)
			if err != nil {
				t.Fatal(err)
			}
			var message waE2E.Message
			if err = proto.Unmarshal(wire, &message); err != nil {
				t.Fatal("actual original message wire rejected", err)
			}
			var media proto.Message
			switch row.Name {
			case "quoted-group-text":
				text := message.GetExtendedTextMessage()
				if text.GetText() != "actual quoted group reply" || text.GetContextInfo().GetStanzaID() != "original-quoted-id" || text.GetContextInfo().GetQuotedMessage().GetConversation() != "older text" {
					t.Fatal("actual reply consumer lost quote/context")
				}
				media = text
			case "view-once-image":
				image := message.GetViewOnceMessage().GetMessage().GetImageMessage()
				if image.GetCaption() != "图片 — original caption" || image.GetFileLength() != 4096 || !image.GetViewOnce() || image.GetWidth() != 480 || !bytes.Equal(image.GetMediaKey(), []byte{0, 1, 128, 255}) {
					t.Fatal("actual protected media consumer lost image semantics")
				}
				media = image
			case "original-document":
				document := message.GetDocumentMessage()
				if document.GetFileName() != "中文合同.pdf" || document.GetMimetype() != "application/pdf" || document.GetPageCount() != 4 || document.GetFileLength() != 8192 {
					t.Fatal("actual document consumer lost file metadata")
				}
				media = document
			case "voice-note":
				audio := message.GetAudioMessage()
				if !audio.GetPTT() || audio.GetSeconds() != 3 || audio.GetMimetype() != "audio/ogg; codecs=opus" || !bytes.Equal(audio.GetWaveform(), []byte{0, 1, 128, 255}) {
					t.Fatal("actual voice consumer lost codec/timing/waveform")
				}
				media = audio
			case "ephemeral-video":
				video := message.GetEphemeralMessage().GetMessage().GetVideoMessage()
				if video.GetWidth() != 1920 || video.GetHeight() != 1080 || !video.GetGifPlayback() || video.GetSeconds() != 6 || video.GetContextInfo().GetExpiration() != 86400 {
					t.Fatal("actual ephemeral video consumer lost display/expiration")
				}
				media = video
			case "edit-protocol":
				edit := message.GetProtocolMessage()
				if edit.GetType() != waE2E.ProtocolMessage_MESSAGE_EDIT || edit.GetEditedMessage().GetConversation() != "corrected original message" || edit.GetKey().GetID() != "original-quoted-id" || edit.GetTimestampMS() != 1700000000123 {
					t.Fatal("actual edit consumer lost discriminator/target/time")
				}
				media = edit
			case "reaction-protocol":
				reaction := message.GetReactionMessage()
				if reaction.GetText() != "✅" || reaction.GetKey().GetID() != "original-quoted-id" || reaction.GetSenderTimestampMS() != 1700000000456 {
					t.Fatal("actual reaction consumer lost original target/time")
				}
				media = reaction
			default:
				t.Fatal("unowned wire profile", row.Name)
			}
			media.ProtoReflect().SetUnknown(nestedUnknown)
			message.ProtoReflect().SetUnknown(unknown)
			serialized, err := proto.Marshal(&message)
			if err != nil {
				t.Fatal(err)
			}
			// Two cold transport hops must retain future outer and nested fields.
			var cold waE2E.Message
			if err = proto.Unmarshal(serialized, &cold); err != nil || !bytes.Equal(cold.ProtoReflect().GetUnknown(), unknown) || !proto.Equal(&message, &cold) {
				t.Fatal("cold forwarding lost original semantics or future fields", err)
			}
			serialized, err = proto.Marshal(&cold)
			if err != nil {
				t.Fatal(err)
			}
			forwarded = append(forwarded, base64.StdEncoding.EncodeToString(serialized))
		})
	}
	if t.Failed() {
		return
	}
	decoded := originalMessageWires(t, forwarded)
	for i, row := range decoded {
		var got, want any
		if json.Unmarshal(row.Expected, &got) != nil || json.Unmarshal(source[i].Expected, &want) != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("native cold-forwarded known fields cannot be consumed by original peer %s: %s vs %s", row.Name, row.Expected, source[i].Expected)
		}
	}
}
