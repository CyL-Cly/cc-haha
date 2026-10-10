package armadillo_test

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"go.mau.fi/whatsmeow/proto/waArmadilloBackupCommon"
	"go.mau.fi/whatsmeow/proto/waArmadilloBackupMessage"
	"go.mau.fi/whatsmeow/proto/waArmadilloICDC"
	"go.mau.fi/whatsmeow/proto/waArmadilloMiTransportAdminMessage"
	"go.mau.fi/whatsmeow/proto/waArmadilloTransportEvent"
	"go.mau.fi/whatsmeow/proto/waBotMetadata"
	"go.mau.fi/whatsmeow/proto/waCommonParameterised"
	"go.mau.fi/whatsmeow/proto/waConsumerApplicationParameterised"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waE2EGuest"
	"go.mau.fi/whatsmeow/proto/waEphemeral"
	"go.mau.fi/whatsmeow/proto/waFingerprint"
	"go.mau.fi/whatsmeow/proto/waGroupHistory"
	"go.mau.fi/whatsmeow/proto/waMediaEntryData"
	"go.mau.fi/whatsmeow/proto/waQuickPromotionSurfaces"
	"go.mau.fi/whatsmeow/proto/waReporting"
	"go.mau.fi/whatsmeow/proto/waRoutingInfo"
	"go.mau.fi/whatsmeow/proto/waWinUIApi"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type publicWireCase struct {
	Name, Message, WireHex string
	ExpectedJSON           json.RawMessage
	CriticalFieldNumbers   map[string]int
	Oneofs                 map[string]string
}

func originalWireCases(t *testing.T) ([]publicWireCase, map[string]string) {
	t.Helper()
	raw, err := os.ReadFile("testdata/public-wire-contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var input struct {
		Cases           []publicWireCase
		NegativeWireHex map[string]string
	}
	if err = json.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	if len(input.Cases) != 17 {
		t.Fatal("original finite public protocol inventory incomplete")
	}
	return input.Cases, input.NegativeWireHex
}

func publicMessage(t *testing.T, fullName string) proto.Message {
	t.Helper()
	factories := map[string]func() proto.Message{
		"WAArmadilloBackupCommon.Subprotocol":                        func() proto.Message { return new(waArmadilloBackupCommon.Subprotocol) },
		"WAArmadilloBackupMessage.BackupMessage":                     func() proto.Message { return new(waArmadilloBackupMessage.BackupMessage) },
		"WAArmadilloICDC.ICDCIdentityList":                           func() proto.Message { return new(waArmadilloICDC.ICDCIdentityList) },
		"WAArmadilloMiTransportAdminMessage.MiTransportAdminMessage": func() proto.Message { return new(waArmadilloMiTransportAdminMessage.MiTransportAdminMessage) },
		"WAArmadilloTransportEvent.TransportEvent":                   func() proto.Message { return new(waArmadilloTransportEvent.TransportEvent) },
		"WABotMetadata.BotMetadata":                                  func() proto.Message { return new(waBotMetadata.BotMetadata) },
		"WACommonParameterised.MessageText":                          func() proto.Message { return new(waCommonParameterised.MessageText) },
		"WAConsumerApplicationParameterised.ConsumerApplication":     func() proto.Message { return new(waConsumerApplicationParameterised.ConsumerApplication) },
		"WAWebProtobufsE2EGuest.Message":                             func() proto.Message { return new(waE2EGuest.Message) },
		"WAWebProtobufsEphemeral.EphemeralSetting":                   func() proto.Message { return new(waEphemeral.EphemeralSetting) },
		"WAFingerprint.CombinedFingerprint":                          func() proto.Message { return new(waFingerprint.CombinedFingerprint) },
		"WAWebProtobufsGroupHistory.GroupHistoryWithMessageBytes":    func() proto.Message { return new(waGroupHistory.GroupHistoryWithMessageBytes) },
		"WAMediaEntryData.MediaEntry":                                func() proto.Message { return new(waMediaEntryData.MediaEntry) },
		"WAWebProtobufsQuickPromotionSurfaces.QP.FilterClause":       func() proto.Message { return new(waQuickPromotionSurfaces.QP_FilterClause) },
		"WAWebProtobufsReporting.Config":                             func() proto.Message { return new(waReporting.Config) },
		"WAWebProtobufsRoutingInfo.RoutingInfo":                      func() proto.Message { return new(waRoutingInfo.RoutingInfo) },
		"WAWinUIApi.PositronData":                                    func() proto.Message { return new(waWinUIApi.PositronData) },
	}
	factory := factories[fullName]
	if factory == nil {
		t.Fatal("unimplemented public protocol input", fullName)
	}
	return factory()
}

func TestPublicProtocolOriginalWireConsumers(t *testing.T) {
	cases, negatives := originalWireCases(t)
	for _, input := range cases {
		t.Run(input.Name, func(t *testing.T) {
			wire, err := hex.DecodeString(input.WireHex)
			if err != nil {
				t.Fatal(err)
			}
			message := publicMessage(t, input.Message)
			if err := proto.Unmarshal(wire, message); err != nil {
				t.Fatal("original pre-encoded public wire rejected", err)
			}
			data, err := protojson.Marshal(message)
			if err != nil {
				t.Fatal(err)
			}
			var got, want any
			if json.Unmarshal(data, &got) != nil || json.Unmarshal(input.ExpectedJSON, &want) != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("independent schema/wire contract\nwant %s\ngot %s", input.ExpectedJSON, data)
			}
			view := message.ProtoReflect()
			if string(view.Descriptor().FullName()) != input.Message {
				t.Fatal("registered descriptor selected wrong public message")
			}
			for name, number := range input.CriticalFieldNumbers {
				field := view.Descriptor().Fields().ByName(protoreflect.Name(name))
				if field == nil || int(field.Number()) != number {
					t.Fatal("public wire discriminator/critical field drift", name, number)
				}
			}
			for name, active := range input.Oneofs {
				oneof := view.Descriptor().Oneofs().ByName(protoreflect.Name(name))
				if oneof == nil || view.WhichOneof(oneof) == nil || string(view.WhichOneof(oneof).Name()) != active {
					t.Fatal("duplicate oneof field failed last-member contract", name, active)
				}
			}
			canonical, err := (proto.MarshalOptions{Deterministic: true}).Marshal(message)
			if err != nil {
				t.Fatal(err)
			}
			forward := publicMessage(t, input.Message)
			if err = proto.Unmarshal(canonical, forward); err != nil || !proto.Equal(message, forward) {
				t.Fatal("wire forwarding changed message/oneof/presence", err)
			}
			unknown, err := hex.DecodeString(negatives["unknownField100"])
			if err != nil {
				t.Fatal(err)
			}
			if err := proto.Unmarshal(append(append([]byte(nil), canonical...), unknown...), forward); err != nil {
				t.Fatal("future public field prevented forwarding", err)
			}
			if !bytes.Equal(forward.ProtoReflect().GetUnknown(), unknown) {
				t.Fatal("opaque future protocol field was lost")
			}
			forwardWire, err := proto.Marshal(forward)
			if err != nil {
				t.Fatal(err)
			}
			if err := proto.Unmarshal(forwardWire, message); err != nil || !bytes.Equal(message.ProtoReflect().GetUnknown(), unknown) {
				t.Fatal("second transport hop lost unknown field", err)
			}
			proto.Reset(message)
			set := 0
			message.ProtoReflect().Range(func(protoreflect.FieldDescriptor, protoreflect.Value) bool { set++; return true })
			if set != 0 || len(message.ProtoReflect().GetUnknown()) != 0 {
				t.Fatal("reused message retained prior peer fields or unknown bytes")
			}
		})
	}
}

func TestPublicProtocolMalformedFramesAndRequiredFields(t *testing.T) {
	_, negatives := originalWireCases(t)
	for _, name := range []string{"zeroFieldTag", "truncatedLength", "unclosedVarint"} {
		t.Run(name, func(t *testing.T) {
			wire, err := hex.DecodeString(negatives[name])
			if err != nil {
				t.Fatal(err)
			}
			if err := proto.Unmarshal(wire, new(waArmadilloBackupCommon.Subprotocol)); err == nil {
				t.Fatal("malformed public frame accepted")
			}
		})
	}
	if err := proto.Unmarshal(nil, new(waQuickPromotionSurfaces.QP_FilterClause)); err == nil {
		t.Fatal("required promotion clause discriminator missing")
	}
	if _, err := proto.Marshal(new(waQuickPromotionSurfaces.QP_Filter)); err == nil {
		t.Fatal("required filter name/client fallback silently emitted")
	}
}

func TestPublicProtocolGroupHistoryParentMessageDecoder(t *testing.T) {
	cases, _ := originalWireCases(t)
	var input publicWireCase
	for _, value := range cases {
		if value.Name == "group-history-opaque-message-and-parent-key" {
			input = value
		}
	}
	wire, err := hex.DecodeString(input.WireHex)
	if err != nil {
		t.Fatal(err)
	}
	var history waGroupHistory.GroupHistoryWithMessageBytes
	if err := proto.Unmarshal(wire, &history); err != nil {
		t.Fatal(err)
	}
	// This is the actual parent WhatsApp message decoder API also used before
	// message event consumers. It does not claim a physical socket/Signal flow.
	var message waE2E.Message
	if len(history.Messages) != 1 || proto.Unmarshal(history.Messages[0].MessageBytes, &message) != nil || message.GetConversation() != "offline group archive" {
		t.Fatal("archived opaque body failed actual parent Message decoding")
	}
}
