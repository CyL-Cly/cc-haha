package whatsappcandidate

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"testing"

	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

func publicABIEnumDescriptor(t *testing.T, value protoreflect.Enum) *descriptorpb.EnumDescriptorProto {
	t.Helper()
	method := reflect.ValueOf(value).MethodByName("EnumDescriptor")
	if !method.IsValid() {
		t.Fatal("public enum lost legacy descriptor ABI", value.Descriptor().FullName())
	}
	result := method.Call(nil)
	reader, err := gzip.NewReader(bytes.NewReader(result[0].Bytes()))
	if err != nil {
		t.Fatal("legacy enum descriptor is not gzip", err)
	}
	wire, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	var file descriptorpb.FileDescriptorProto
	if err := proto.Unmarshal(wire, &file); err != nil {
		t.Fatal("legacy enum descriptor is not physical FileDescriptorProto", err)
	}
	path := result[1].Interface().([]int)
	if len(path) == 0 {
		t.Fatal("legacy enum descriptor has empty path")
	}
	enumerations := file.GetEnumType()
	children := file.GetMessageType()
	for _, index := range path[:len(path)-1] {
		if index < 0 || index >= len(children) {
			t.Fatal("legacy enum descriptor parent path outside schema", path)
		}
		parent := children[index]
		children, enumerations = parent.GetNestedType(), parent.GetEnumType()
	}
	index := path[len(path)-1]
	if index < 0 || index >= len(enumerations) {
		t.Fatal("legacy enum descriptor index outside schema", path)
	}
	return enumerations[index]
}

func assertPublicABIEnum(t *testing.T, kind protoreflect.EnumType, symbols map[string]int32) {
	t.Helper()
	descriptor := kind.Descriptor()
	legacy := publicABIEnumDescriptor(t, kind.New(0))
	if legacy.GetName() != string(descriptor.Name()) {
		t.Fatal("legacy enum descriptor selects different symbol table", legacy.GetName(), descriptor.FullName())
	}
	legacyValues := map[string]int32{}
	for _, value := range legacy.GetValue() {
		legacyValues[value.GetName()] = value.GetNumber()
	}
	for symbol, number := range symbols {
		nativeSymbol := symbol
		// These exact pinned schema names have distinct native textual APIs;
		// their original symbolic input still selects the same numeric wire.
		// Do not claim public textual ABI equivalence for these exact aliases.
		aliases := map[string]string{
			"WAWebProtobufsAICommon.BotModeSelectionMetadata.BotUserSelectionMode.UNKNOWN_MODE":   "DEFAULT_MODE",
			"WAWebProtobufsAICommon.BotModeSelectionMetadata.BotUserSelectionMode.REASONING_MODE": "THINK_HARD_MODE",
			"WAWebProtobufsE2E.KeepType.UNKNOWN":                                                  "UNKNOWN_KEEP_TYPE",
			"WAWebProtobufsE2E.PollContentType.UNKNOWN":                                           "UNKNOWN_POLL_CONTENT_TYPE",
		}
		if alias := aliases[string(descriptor.FullName())+"."+symbol]; alias != "" {
			nativeSymbol = alias
			t.Log("pinned native textual ABI differs from original symbol; numeric wire stays shared", symbol, nativeSymbol, number)
		}
		if actual, present := legacyValues[nativeSymbol]; !present || actual != number {
			t.Errorf("original symbol-number pair drift in public descriptor %s.%s=%d original=%d", descriptor.FullName(), nativeSymbol, actual, number)
		}
		value := kind.New(protoreflect.EnumNumber(number))
		if value.Number() != protoreflect.EnumNumber(number) || value.Type() != kind || value.Descriptor().FullName() != descriptor.FullName() {
			t.Fatal("public enum Type/Number/Descriptor changed actual parsed value", descriptor.FullName(), number)
		}
		text := reflect.ValueOf(value).MethodByName("String").Call(nil)[0].String()
		if text != nativeSymbol {
			t.Errorf("public symbolic rendering differs from actual original enum %s number=%d native=%s original=%s", descriptor.FullName(), number, text, symbol)
		}
		pointer := reflect.ValueOf(value).MethodByName("Enum").Call(nil)[0]
		if pointer.Elem().Interface().(protoreflect.Enum).Number() != value.Number() {
			t.Fatal("public Enum pointer lost original decoded number", descriptor.FullName())
		}
		decode, ok := pointer.Interface().(interface{ UnmarshalJSON([]byte) error })
		if !ok {
			t.Fatal("public enum lost JSON ABI", descriptor.FullName())
		}
		if err := decode.UnmarshalJSON(nodeJSON(nativeSymbol)); err != nil || pointer.Elem().Interface().(protoreflect.Enum).Number() != value.Number() {
			t.Fatal("symbolic original enum JSON does not restore actual number", descriptor.FullName(), symbol, err)
		}
		const unknown int32 = 2147483647
		if err := decode.UnmarshalJSON([]byte(strconv.FormatInt(int64(unknown), 10))); err != nil {
			t.Fatal("public JSON ABI discarded future numeric enum", descriptor.FullName(), err)
		}
		future := pointer.Elem().Interface().(protoreflect.Enum)
		if future.Number() != protoreflect.EnumNumber(unknown) || pointer.Elem().MethodByName("String").Call(nil)[0].String() != "2147483647" {
			t.Fatal("public enum unknown numeric rendering lost wire value", descriptor.FullName(), future.Number())
		}
		for _, invalid := range []string{`"UNRECOGNIZED_PUBLIC_ABI_SYMBOL"`, `{}`, `[]`, `true`, `1.5`, `2147483648`, `"` + symbol + `" trailing`} {
			if err := decode.UnmarshalJSON([]byte(invalid)); err == nil || pointer.Elem().Interface().(protoreflect.Enum).Number() != future.Number() {
				t.Fatal("invalid enum JSON accepted or mutated previous value", descriptor.FullName(), invalid, err)
			}
		}
	}
}

func clearPublicABIUnknowns(message protoreflect.Message) {
	message.SetUnknown(nil)
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		if field.IsMap() && field.MapValue().Message() != nil {
			value.Map().Range(func(_ protoreflect.MapKey, item protoreflect.Value) bool {
				clearPublicABIUnknowns(item.Message())
				return true
			})
		} else if field.IsList() && field.Message() != nil {
			for i := 0; i < value.List().Len(); i++ {
				clearPublicABIUnknowns(value.List().Get(i).Message())
			}
		} else if field.Message() != nil {
			clearPublicABIUnknowns(value.Message())
		}
		return true
	})
}

func assertPublicABIMessageDebugReset(t *testing.T, kind protoreflect.MessageType, wire []byte) []byte {
	t.Helper()
	nilMessage := reflect.Zero(reflect.TypeOf(kind.New().Interface())).Interface().(proto.Message)
	nilView := nilMessage.ProtoReflect()
	if nilView.IsValid() || nilView.Descriptor().FullName() != kind.Descriptor().FullName() || reflect.ValueOf(nilMessage).MethodByName("String").Call(nil)[0].String() != "<nil>" {
		t.Fatal("absent public message reflection/debug ABI lost nil identity", kind.Descriptor().FullName())
	}
	message := kind.New().Interface()
	if err := proto.Unmarshal(wire, message); err != nil {
		t.Fatal("public ABI rejected independent populated wire", kind.Descriptor().FullName(), err)
	}
	// Text syntax is a known-field projection. Prototext does not recover
	// opaque numeric fields from debug text, so their preservation is verified
	// separately by physical binary forwarding and reset below.
	known := proto.Clone(message)
	clearPublicABIUnknowns(known.ProtoReflect())
	debug := reflect.ValueOf(known).MethodByName("String").Call(nil)[0].String()
	parsed := kind.New().Interface()
	if err := (prototext.UnmarshalOptions{AllowPartial: true}).Unmarshal([]byte(debug), parsed); err != nil || !proto.Equal(known, parsed) {
		t.Fatalf("public debug text changed actual values/presence/oneofs %s %.500s %v", kind.Descriptor().FullName(), debug, err)
	}
	opaque := protowire.AppendTag(bytes.Clone(message.ProtoReflect().GetUnknown()), 65000, protowire.BytesType)
	opaque = protowire.AppendBytes(opaque, []byte("public ABI future peer payload"))
	message.ProtoReflect().SetUnknown(opaque)
	forward, err := proto.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	cold := kind.New().Interface()
	if err := proto.Unmarshal(forward, cold); err != nil || !proto.Equal(message, cold) || !bytes.Equal(cold.ProtoReflect().GetUnknown(), opaque) {
		t.Fatal("cold public ABI forwarding lost fields/oneofs/unknown", kind.Descriptor().FullName(), err)
	}
	// Invoke the generated public method rather than a no-op proto.Reset on a
	// dynamic shell, then inspect every observable store it must clear.
	reflect.ValueOf(message).MethodByName("Reset").Call(nil)
	set := 0
	message.ProtoReflect().Range(func(protoreflect.FieldDescriptor, protoreflect.Value) bool { set++; return true })
	if set != 0 || len(message.ProtoReflect().GetUnknown()) != 0 || !proto.Equal(message, kind.New().Interface()) {
		t.Fatal("public Reset retained populated wire fields or opaque bytes", kind.Descriptor().FullName())
	}
	for i := 0; i < kind.Descriptor().Oneofs().Len(); i++ {
		if message.ProtoReflect().WhichOneof(kind.Descriptor().Oneofs().Get(i)) != nil {
			t.Fatal("public Reset retained oneof discriminator", kind.Descriptor().FullName())
		}
	}
	if err := proto.Unmarshal(wire, message); err != nil || !proto.Equal(message, protoMessageFromWire(t, kind, wire)) {
		t.Fatal("reusing Reset message kept previous peer state", kind.Descriptor().FullName(), err)
	}
	return forward
}

func protoMessageFromWire(t *testing.T, kind protoreflect.MessageType, wire []byte) proto.Message {
	t.Helper()
	message := kind.New().Interface()
	if err := proto.Unmarshal(wire, message); err != nil {
		t.Fatal(err)
	}
	return message
}

func TestActualOriginalProtocolPublicABIEnumJSONDebugTextResetAndColdWire(t *testing.T) {
	var source []originalPropertyType
	originalPropertyCorpus(t, nil, &source)
	kinds, _, _ := propertyTypes(source)
	seenEnums := map[protoreflect.FullName]bool{}
	forwarded := []map[string]string{}
	for _, row := range source {
		kind := kinds[row.Type]
		if kind == nil {
			continue // Existing exact original inventory test owns these gaps.
		}
		for name, schema := range row.FieldSchemas {
			field := propertyField(kind.Descriptor(), name)
			if field == nil || field.Enum() == nil || len(schema.Enum) == 0 || seenEnums[field.Enum().FullName()] {
				continue
			}
			seenEnums[field.Enum().FullName()] = true
			enumeration, err := protoregistry.GlobalTypes.FindEnumByName(field.Enum().FullName())
			if err != nil {
				t.Fatal("public field enum type not registered", field.FullName(), err)
			}
			symbols := map[string]int32{}
			for _, pair := range schema.Enum {
				var parts []json.RawMessage
				_ = json.Unmarshal(pair, &parts)
				var symbol string
				var number int32
				if len(parts) != 2 || json.Unmarshal(parts[0], &symbol) != nil || json.Unmarshal(parts[1], &number) != nil {
					t.Fatal("actual original enum symbol-number metadata invalid", row.Type, name)
				}
				symbols[symbol] = number
			}
			t.Run("enum/"+string(field.Enum().FullName()), func(t *testing.T) { assertPublicABIEnum(t, enumeration, symbols) })
		}
		for _, input := range row.Cases {
			if input.SourceError != "" || input.Wire == "" {
				continue
			}
			t.Run("message/"+row.Type, func(t *testing.T) {
				wire, err := base64.StdEncoding.DecodeString(input.Wire)
				if err != nil {
					t.Fatal(err)
				}
				forward := assertPublicABIMessageDebugReset(t, kind, wire)
				forwarded = append(forwarded, map[string]string{"type": row.Type, "name": "public ABI debug/reset cold reuse", "wire": base64.StdEncoding.EncodeToString(forward), "expected": string(nodeJSON(input.Known))})
			})
			break // One actual populated peer wire per message constructor.
		}
		if len(row.Cases) == 0 && row.DefaultWire != nil && row.DefaultError == "" {
			t.Run("empty-schema/"+row.Type, func(t *testing.T) {
				wire, err := base64.StdEncoding.DecodeString(*row.DefaultWire)
				if err != nil {
					t.Fatal(err)
				}
				forward := assertPublicABIMessageDebugReset(t, kind, wire)
				forwarded = append(forwarded, map[string]string{"type": row.Type, "name": "public empty schema reset clears actual future wire", "wire": base64.StdEncoding.EncodeToString(forward), "expected": "{}"})
			})
		}
	}
	if t.Failed() {
		return
	}
	var received []struct {
		Type, Name string
		Known      json.RawMessage
	}
	originalPropertyCorpus(t, forwarded, &received)
	if len(received) != len(forwarded) {
		t.Fatal("actual original ABI forwarding receipts missing", len(received), len(forwarded))
	}
	for index, row := range received {
		var actual any
		_ = json.Unmarshal(row.Known, &actual)
		if !samePropertyJSON(actual, []byte(forwarded[index]["expected"])) && !knownReportingPresenceDifference(row.Type, row.Known, []byte(forwarded[index]["expected"])) {
			t.Errorf("actual original decoder rejected ABI cold forwarding %s: %s expected %s", row.Type, row.Known, forwarded[index]["expected"])
		}
	}
	t.Log(fmt.Sprintf("actual original public ABI: %d enum symbol tables, %d populated message debug/reset/binary/Node receipts", len(seenEnums), len(forwarded)))
}
