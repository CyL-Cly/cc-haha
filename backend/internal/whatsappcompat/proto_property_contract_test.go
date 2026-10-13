package whatsappcandidate

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

type originalPropertyCase struct {
	Synthetic                                  bool
	Field, Scalar, MessageType, Selector, Wire string
	Expected                                   json.RawMessage
	Present                                    bool
	Known                                      map[string]json.RawMessage
	SourceError                                string
}
type originalPropertyType struct {
	Type         string
	Defaults     map[string]json.RawMessage
	Prototypes   map[string]json.RawMessage
	FieldSchemas map[string]struct {
		Tag                     int32
		Type                    string
		Repeated, Map, Optional bool
		KeyType                 string
		Oneof                   *string
		Enum                    []json.RawMessage
		Default                 json.RawMessage
	}
	Authority    map[string]string
	DefaultWire  *string
	DefaultError string
	Cases        []originalPropertyCase
}

func originalPropertyCorpus(t *testing.T, input any, target any) {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual original protocol property corpus requires Source Bun")
	}
	home := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, bun, "--no-env-file", nodeSourceFixturePath("node_property_contract.fixture.ts"))
	child.Dir = home
	child.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_WHATSAPP_LEAF=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	object := map[string]any{"homeDir": home}
	if input != nil {
		object["wires"] = input
	}
	child.Stdin = bytes.NewReader(nodeJSON(object))
	var stderr bytes.Buffer
	child.Stderr = &stderr
	output, err := child.Output()
	decodeErr := json.Unmarshal(output, target)
	if err != nil || decodeErr != nil {
		t.Fatalf("actual original property codec %v JSON=%v bytes=%d %.2000s %.2000s", err, decodeErr, len(output), stderr.String(), output)
	}
}

func propertyName(name string) string { return strings.ToLower(strings.ReplaceAll(name, "_", "")) }
func propertyField(descriptor protoreflect.MessageDescriptor, name string) protoreflect.FieldDescriptor {
	fields := descriptor.Fields()
	for index := 0; index < fields.Len(); index++ {
		field := fields.Get(index)
		if propertyName(field.JSONName()) == propertyName(name) || propertyName(string(field.Name())) == propertyName(name) {
			return field
		}
	}
	return nil
}
func propertyTypes(source []originalPropertyType) (map[string]protoreflect.MessageType, map[protoreflect.FullName]map[string]string, []string) {
	byName := map[string][]protoreflect.MessageType{}
	protoregistry.GlobalTypes.RangeMessages(func(kind protoreflect.MessageType) bool {
		if strings.HasPrefix(string(kind.Descriptor().FullName()), "google.") {
			return true
		}
		name := string(kind.Descriptor().Name())
		byName[name] = append(byName[name], kind)
		return true
	})
	matched := map[string]protoreflect.MessageType{}
	trusted := map[string]bool{}
	names := map[protoreflect.FullName]map[string]string{}
	pending := map[string][]protoreflect.MessageType{}
	for _, row := range source {
		parts := strings.Split(row.Type, ".")
		candidates := byName[parts[len(parts)-1]]
		best, score, ambiguous := protoreflect.MessageType(nil), -1, false
		for _, candidate := range candidates {
			count := 0
			for field := range row.Defaults {
				if propertyField(candidate.Descriptor(), field) != nil {
					count++
				}
			}
			if strings.HasSuffix(string(candidate.Descriptor().FullName()), "."+row.Type) {
				count += 10000
			}
			if strings.TrimPrefix(string(candidate.Descriptor().FullName()), string(candidate.Descriptor().ParentFile().Package())+".") == row.Type {
				count += 5000
			}
			if row.Type == "Message" && strings.HasPrefix(string(candidate.Descriptor().FullName()), "WAWebProtobufsE2E.") {
				count += 100000
			}
			if count > score {
				best, score, ambiguous = candidate, count, false
			} else if count == score {
				ambiguous = true
			}
		}
		if best == nil || ambiguous {
			pending[row.Type] = candidates
			continue
		}
		matched[row.Type] = best
		if len(candidates) == 1 || row.Type == "Message" {
			trusted[row.Type] = true
		}
	}
	// Resolve old JS's flattened/nested aliases through the actual native
	// parent's message field. This binds a name to a production descriptor,
	// not to an arbitrarily selected same-basename generated constructor.
	for round := 0; round < len(source); round++ {
		progress := false
		for _, row := range source {
			parent := matched[row.Type]
			if parent == nil || !trusted[row.Type] {
				continue
			}
			for _, input := range row.Cases {
				if input.MessageType == "" || trusted[input.MessageType] {
					continue
				}
				field := propertyField(parent.Descriptor(), input.Field)
				if field == nil || field.Message() == nil {
					continue
				}
				kind, err := protoregistry.GlobalTypes.FindMessageByName(field.Message().FullName())
				if err == nil {
					matched[input.MessageType] = kind
					trusted[input.MessageType] = true
					delete(pending, input.MessageType)
					progress = true
				}
			}
		}
		if !progress {
			break
		}
	}
	for _, row := range source {
		best := matched[row.Type]
		if best == nil {
			continue
		}
		mapping := map[string]string{}
		for field := range row.Defaults {
			if native := propertyField(best.Descriptor(), field); native != nil {
				mapping[string(native.Name())] = field
			}
		}
		for _, input := range row.Cases {
			if input.Selector != "" {
				if field := propertyField(best.Descriptor(), input.Field); field != nil {
					mapping["selector:"+string(field.Name())] = input.Selector
				}
			}
		}
		names[best.Descriptor().FullName()] = mapping
	}
	missing := []string{}
	for name, candidates := range pending {
		full := []string{}
		for _, candidate := range candidates {
			full = append(full, string(candidate.Descriptor().FullName()))
		}
		sort.Strings(full)
		missing = append(missing, fmt.Sprintf("%s: candidates=%v", name, full))
	}
	sort.Strings(missing)
	return matched, names, missing
}

func generatedPropertyMethod(message reflect.Type, field protoreflect.FieldDescriptor) string {
	structure := message.Elem()
	for index := 0; index < structure.NumField(); index++ {
		member := structure.Field(index)
		for _, tag := range strings.Split(member.Tag.Get("protobuf"), ",") {
			if tag == "name="+string(field.Name()) {
				return "Get" + member.Name
			}
		}
	}
	// Oneof members live in generated wrapper types. The public field getters
	// use protoc's exported JSON name; match its acronym spelling through the
	// real method inventory rather than inventing one member's implementation.
	for index := 0; index < message.NumMethod(); index++ {
		method := message.Method(index)
		if strings.HasPrefix(method.Name, "Get") && propertyName(strings.TrimPrefix(method.Name, "Get")) == propertyName(field.JSONName()) {
			return method.Name
		}
	}
	return ""
}

func messagePropertyJSON(message protoreflect.Message, names map[protoreflect.FullName]map[string]string) any {
	output := map[string]any{}
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		name := names[message.Descriptor().FullName()][string(field.Name())]
		if name == "" {
			name = field.JSONName()
		}
		output[name] = reflectedPropertyJSON(reflect.ValueOf(value.Interface()), names)
		if selector := names[message.Descriptor().FullName()]["selector:"+string(field.Name())]; selector != "" {
			output[selector] = name
		}
		return true
	})
	return output
}
func reflectedPropertyJSON(value reflect.Value, names map[protoreflect.FullName]map[string]string) any {
	if value.Kind() == reflect.Interface {
		if value.IsNil() {
			return nil
		}
		return reflectedPropertyJSON(value.Elem(), names)
	}
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return nil
		}
		if message, ok := value.Interface().(proto.Message); ok {
			return messagePropertyJSON(message.ProtoReflect(), names)
		}
	}
	if value.CanInterface() {
		if message, ok := value.Interface().(protoreflect.Message); ok {
			return messagePropertyJSON(message, names)
		}
		if list, ok := value.Interface().(protoreflect.List); ok {
			output := []any{}
			for index := 0; index < list.Len(); index++ {
				output = append(output, reflectedPropertyJSON(reflect.ValueOf(list.Get(index).Interface()), names))
			}
			return output
		}
	}
	switch value.Kind() {
	case reflect.Slice:
		if value.Type().Elem().Kind() == reflect.Uint8 {
			return base64.StdEncoding.EncodeToString(value.Bytes())
		}
		output := []any{}
		for index := 0; index < value.Len(); index++ {
			output = append(output, reflectedPropertyJSON(value.Index(index), names))
		}
		return output
	case reflect.Map:
		output := map[string]any{}
		for _, key := range value.MapKeys() {
			output[fmt.Sprint(key.Interface())] = reflectedPropertyJSON(value.MapIndex(key), names)
		}
		return output
	case reflect.Int64:
		return strconv.FormatInt(value.Int(), 10)
	case reflect.Uint64:
		return strconv.FormatUint(value.Uint(), 10)
	case reflect.Int, reflect.Int32:
		return value.Int()
	case reflect.Uint, reflect.Uint32:
		return value.Uint()
	case reflect.Float32, reflect.Float64:
		if math.IsNaN(value.Float()) {
			return "NaN"
		}
		if math.IsInf(value.Float(), 1) {
			return "Infinity"
		}
		if math.IsInf(value.Float(), -1) {
			return "-Infinity"
		}
		return value.Float()
	default:
		return value.Interface()
	}
}

func samePropertyJSON(got any, expected json.RawMessage) bool {
	var actual, want any
	return json.Unmarshal(nodeJSON(got), &actual) == nil && json.Unmarshal(expected, &want) == nil && reflect.DeepEqual(actual, want)
}

// Source proto3 optional properties expose null when absent. Public Go getters
// are typed; their nil defaults are compared to values independently produced
// by the original codec using the original schema's first declared enum value.
// Raw prototype/presence differences remain in the explicit inventory matrix.
func knownInternalProtocolType(name string) bool {
	switch name {
	case "ChatRowOpaqueData", "ChatRowOpaqueData.DraftMessage", "ChatRowOpaqueData.DraftMessage.CtwaContextData", "ChatRowOpaqueData.DraftMessage.CtwaContextLinkData", "MsgOpaqueData", "MsgOpaqueData.EventLocation", "MsgOpaqueData.PollOption", "MsgOpaqueData.PollVoteSnapshot", "MsgOpaqueData.PollVotesSnapshot", "MsgRowOpaqueData":
		return true
	}
	return false
}

// These are audited schema gaps, not claimed typed property parity. Each
// affected wire is still decoded by the actual native constructor, cold
// forwarded twice, and independently decoded by the original Node codec.
func knownOpaqueProperty(typ, field string) bool {
	switch typ + "." + field {
	case "BotMetadata.avatarMetadata", "Message.AudioMessage.mediaKeyDomain", "Message.DocumentMessage.mediaKeyDomain", "Message.ImageMessage.mediaKeyDomain", "Message.MMSThumbnailMetadata.mediaKeyDomain", "Message.StickerMessage.mediaKeyDomain", "Message.VideoMessage.mediaKeyDomain", "Message.PaymentExtendedMetadata.messageParamsJson", "SyncActionValue.businessBroadcastAssociationAction", "SyncActionValue.MusicUserIdAction.musicUserIdMap", "Message.pollResultSnapshotMessageV3":
		return true
	}
	return false
}
func hasUnknownField(wire []byte, number protowire.Number) bool {
	for len(wire) > 0 {
		n, typ, size := protowire.ConsumeTag(wire)
		if size < 0 {
			return false
		}
		wire = wire[size:]
		size = protowire.ConsumeFieldValue(n, typ, wire)
		if size < 0 {
			return false
		}
		if n == number {
			return true
		}
		wire = wire[size:]
	}
	return false
}
func appendGetterChildWires(t *testing.T, forwarded *[]map[string]string, value reflect.Value, input originalPropertyCase) {
	t.Helper()
	if value.Kind() == reflect.Slice {
		var expected []json.RawMessage
		if json.Unmarshal(input.Expected, &expected) != nil || len(expected) != value.Len() {
			t.Fatal("Source repeated property shape differs", input.Field)
		}
		for i := 0; i < value.Len(); i++ {
			child := input
			child.Expected = expected[i]
			child.Field = fmt.Sprintf("%s[%d] getter", input.Field, i)
			appendGetterChildWires(t, forwarded, value.Index(i), child)
		}
		return
	}
	if value.IsNil() {
		t.Fatal("actual getter lost Source child", input.Field)
	}
	message, ok := value.Interface().(proto.Message)
	if !ok {
		t.Fatal("actual getter child is not native protobuf", input.Field)
	}
	raw, err := proto.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	*forwarded = append(*forwarded, map[string]string{"type": input.MessageType, "name": input.Field + " getter", "wire": base64.StdEncoding.EncodeToString(raw), "expected": string(input.Expected)})
}

// The generated legacy descriptor remains a public compatibility API. Validate
// its physical gzip/schema payload against the independent original Node
// schema used to produce the wires, rather than calling it only for counters.
func assertOriginalPublicSchemaDescriptor(t *testing.T, message proto.Message, row originalPropertyType) {
	t.Helper()
	returned := reflect.ValueOf(message).MethodByName("Descriptor").Call(nil)
	raw := returned[0].Bytes()
	path := returned[1].Interface().([]int)
	compressed, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	wire, err := io.ReadAll(compressed)
	compressed.Close()
	if err != nil {
		t.Fatal(err)
	}
	var file descriptorpb.FileDescriptorProto
	if err = proto.Unmarshal(wire, &file); err != nil {
		t.Fatal("public schema payload is not protobuf", err)
	}
	var typ *descriptorpb.DescriptorProto
	children := file.GetMessageType()
	for _, index := range path {
		if index < 0 || index >= len(children) {
			t.Fatal("public schema path outside descriptor")
		}
		typ = children[index]
		children = typ.GetNestedType()
	}
	if typ == nil {
		t.Fatal("public constructor schema type absent")
	}
	for name, original := range row.FieldSchemas {
		if knownOpaqueProperty(row.Type, name) {
			continue
		}
		found := false
		for _, field := range typ.GetField() {
			if propertyName(field.GetName()) == propertyName(name) {
				found = true
				if field.GetNumber() != original.Tag {
					t.Errorf("actual public descriptor tag %s.%s=%d original=%d", row.Type, name, field.GetNumber(), original.Tag)
				}
			}
		}
		if !found {
			t.Error("actual public schema lost original field", row.Type, name)
		}
	}
}

func TestActualOriginalProtocolPropertyCorpusNilDefaultsValuesPresenceAndColdForwarding(t *testing.T) {
	var source []originalPropertyType
	originalPropertyCorpus(t, nil, &source)
	if len(source) < 100 {
		t.Fatal("actual original public protocol inventory unexpectedly empty", len(source))
	}
	kinds, names, missing := propertyTypes(source)
	t.Logf("actual Node inventory %d; matched native types %d; unmatched/ambiguous %d", len(source), len(kinds), len(missing))
	for _, row := range missing {
		if !knownInternalProtocolType(strings.SplitN(row, ":", 2)[0]) {
			t.Error("unreviewed original protocol type lacks native consumer", row)
		} else {
			t.Log("original internal DB type has no native typed API (not a DirectChat/auth consumer)", row)
		}
	}
	forwarded := []map[string]string{}
	for _, row := range source {
		kind := kinds[row.Type]
		if kind == nil {
			continue // The exact inventory failure above stays a failing receipt.
		}
		t.Run(row.Type, func(t *testing.T) {
			message := kind.New().Interface()
			structure := reflect.TypeOf(message)
			assertOriginalPublicSchemaDescriptor(t, message, row)
			for fieldName, expected := range row.Defaults {
				field := propertyField(kind.Descriptor(), fieldName)
				if field == nil {
					if !knownOpaqueProperty(row.Type, fieldName) {
						t.Error("unreviewed original property absent from native schema", fieldName)
					}
					continue
				}
				if message.ProtoReflect().Has(field) {
					t.Error("empty native message unexpectedly has original property", fieldName)
				}
				method := generatedPropertyMethod(structure, field)
				if method == "" {
					t.Error("generated public property consumer missing", fieldName)
					continue
				}
				for _, receiver := range []reflect.Value{reflect.Zero(structure), reflect.ValueOf(message)} {
					result := receiver.MethodByName(method).Call(nil)
					got := reflectedPropertyJSON(result[0], names)
					if !samePropertyJSON(got, expected) {
						t.Errorf("original default %s %s: Go=%.500s Node=%.500s", row.Type, fieldName, nodeJSON(got), expected)
					}
				}
			}
			for _, input := range row.Cases {
				if input.SourceError != "" {
					t.Logf("original codec rejects its external %s field input: %s", input.Field, input.SourceError)
					continue
				}
				field := propertyField(kind.Descriptor(), input.Field)
				typed := field != nil && int32(field.Number()) == row.FieldSchemas[input.Field].Tag
				if !typed && !knownOpaqueProperty(row.Type, input.Field) {
					t.Error("unreviewed field/tag gap", row.Type, input.Field)
				}
				method := ""
				if typed {
					method = generatedPropertyMethod(structure, field)
					if method == "" {
						t.Fatal("actual public field getter missing", input.Field)
					}
				}
				wire, err := base64.StdEncoding.DecodeString(input.Wire)
				if err != nil {
					t.Fatal(err)
				}
				message = kind.New().Interface()
				if err = proto.Unmarshal(wire, message); err != nil {
					t.Error("native consumer rejected original physical wire", input.Field, err)
					continue
				}
				if typed {
					result := reflect.ValueOf(message).MethodByName(method).Call(nil)
					if message.ProtoReflect().Has(field) != input.Present {
						t.Error("actual native property presence differs", input.Field)
					}
					if field.Message() != nil {
						appendGetterChildWires(t, &forwarded, result[0], input)
					} else {
						got := reflectedPropertyJSON(result[0], names)
						if !samePropertyJSON(got, input.Expected) {
							t.Errorf("original physical property %s: Go=%.1000s Node=%.1000s", input.Field, nodeJSON(got), input.Expected)
						}
					}
				} else if !hasUnknownField(message.ProtoReflect().GetUnknown(), protowire.Number(row.FieldSchemas[input.Field].Tag)) {
					t.Error("actual Source field/tag gap not preserved as unknown", input.Field)
				}
				if typed && input.Selector != "" && !input.Synthetic {
					oneof := field.ContainingOneof()
					if oneof == nil || message.ProtoReflect().WhichOneof(oneof) != field {
						t.Error("original selected oneof lost", input.Field, input.Selector)
					}
				}
				unknown := protowire.AppendTag(bytes.Clone(message.ProtoReflect().GetUnknown()), 65000, protowire.BytesType)
				unknown = protowire.AppendBytes(unknown, []byte("future property corpus field"))
				message.ProtoReflect().SetUnknown(unknown)
				canonical, err := proto.Marshal(message)
				if err != nil {
					t.Fatal(err)
				}
				cold := kind.New().Interface()
				if err = proto.Unmarshal(canonical, cold); err != nil || !proto.Equal(message, cold) || !bytes.Equal(cold.ProtoReflect().GetUnknown(), unknown) {
					t.Fatal("cold protocol forwarding changed actual fields or opaque bytes", err)
				}
				canonical, err = proto.Marshal(cold)
				if err != nil {
					t.Fatal(err)
				}
				forwarded = append(forwarded, map[string]string{"type": row.Type, "name": input.Field, "wire": base64.StdEncoding.EncodeToString(canonical), "expected": string(nodeJSON(input.Known))})
			}
		})
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
		t.Fatal("actual original consumer receipts missing", len(received), len(forwarded))
	}
	for index, row := range received {
		var actual any
		if json.Unmarshal(row.Known, &actual) != nil || !samePropertyJSON(actual, []byte(forwarded[index]["expected"])) {
			if knownReportingPresenceDifference(row.Type, row.Known, []byte(forwarded[index]["expected"])) {
				t.Log("documented unused Reporting API loses only Source explicit-default presence", row.Type, row.Name)
				continue
			}
			t.Errorf("original cold consumer %s/%s differs: %s vs %s", row.Type, row.Name, row.Known, forwarded[index]["expected"])
		}
	}
}

func knownReportingPresenceDifference(typ string, actual, want json.RawMessage) bool {
	if typ != "Reportable" {
		return false
	}
	var got, expected map[string]json.RawMessage
	if json.Unmarshal(actual, &got) != nil || json.Unmarshal(want, &expected) != nil {
		return false
	}
	for field, zero := range map[string]string{"minVersion": "0", "maxVersion": "0", "notReportableMinVersion": "0", "never": "false"} {
		if _, present := got[field]; !present && string(expected[field]) == zero {
			delete(expected, field)
			delete(expected, "_"+field)
		}
	}
	return samePropertyJSON(got, nodeJSON(expected))
}

func TestActualOriginalProtocolCompatibilityInventory(t *testing.T) {
	var source []originalPropertyType
	originalPropertyCorpus(t, nil, &source)
	kinds, _, missing := propertyTypes(source)
	rows := []map[string]any{}
	for _, row := range source {
		kind := kinds[row.Type]
		if kind == nil {
			rows = append(rows, map[string]any{"sourceType": row.Type, "difference": "no native descriptor", "source": row.Authority, "fields": row.FieldSchemas})
			continue
		}
		for name, sf := range row.FieldSchemas {
			fd := propertyField(kind.Descriptor(), name)
			record := map[string]any{"sourceType": row.Type, "sourceField": name, "source": row.Authority, "sourceSchema": sf, "sourcePrototype": row.Prototypes[name], "goType": kind.Descriptor().FullName(), "goSchema": kind.Descriptor().ParentFile().Path()}
			if fd == nil {
				record["difference"] = "no typed native field; opaque wire retention to be verified"
				rows = append(rows, record)
				continue
			}
			record["goField"] = map[string]any{"name": fd.Name(), "number": fd.Number(), "kind": fd.Kind().String(), "cardinality": fd.Cardinality().String(), "presence": fd.HasPresence(), "syntax": fd.ParentFile().Syntax().String(), "default": fmt.Sprint(fd.Default().Interface())}
			differences := []string{}
			if sf.Optional && fd.Cardinality() == protoreflect.Required {
				differences = append(differences, "native required versus Source optional")
			}
			if sf.Optional && !sf.Repeated && !sf.Map && !fd.HasPresence() {
				differences = append(differences, "native scalar has no presence versus Source optional")
			}
			wantKind := strings.ToLower(sf.Type)
			if len(sf.Enum) > 0 {
				wantKind = "enum"
			} else if fd.Message() != nil {
				wantKind = "message"
			}
			if !sf.Map && wantKind != fd.Kind().String() {
				differences = append(differences, "kind")
			}
			if sf.Tag != int32(fd.Number()) {
				differences = append(differences, "tag")
			}
			if sf.Repeated != fd.IsList() || sf.Map != fd.IsMap() {
				differences = append(differences, "cardinality")
			}
			if fd.Enum() != nil {
				got := map[string]int32{}
				for i := 0; i < fd.Enum().Values().Len(); i++ {
					v := fd.Enum().Values().Get(i)
					got[string(v.Name())] = int32(v.Number())
				}
				want := map[string]int32{}
				for _, raw := range sf.Enum {
					var pair []json.RawMessage
					if json.Unmarshal(raw, &pair) != nil || len(pair) != 2 {
						t.Fatal("original enum metadata malformed", row.Type, name)
					}
					var key string
					var value int32
					json.Unmarshal(pair[0], &key)
					json.Unmarshal(pair[1], &value)
					want[key] = value
				}
				record["goEnum"] = got
				if !reflect.DeepEqual(got, want) {
					differences = append(differences, "enum inventory")
				}
			}
			if string(row.Prototypes[name]) == "null" && fd.Kind() == protoreflect.EnumKind && int32(fd.Default().Enum()) != 0 {
				differences = append(differences, "absent Node null versus Go typed first-enum default")
			}
			if len(differences) > 0 {
				record["difference"] = differences
				rows = append(rows, record)
			}
		}
	}
	t.Logf("independent Source inventory %d mapped %d unmapped %d difference rows %d", len(source), len(kinds), len(missing), len(rows))
	if path := os.Getenv("CC_HAHA_PROTO_DIFFERENCE_REPORT"); path != "" {
		data, err := json.MarshalIndent(map[string]any{"sourceTypes": len(source), "nativeTypes": len(kinds), "missing": missing, "differences": rows}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, append(data, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
