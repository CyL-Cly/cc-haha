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
	"sort"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

type independentABICorpus struct {
	Authority struct {
		Scope   string
		Schemas []struct{ File, SHA256 string }
	}
	Records []struct {
		Type   string
		Fields map[string]struct {
			Tag           int32
			Type          string
			Repeated, Map bool
			Oneof         *string
		}
		Defaults map[string]json.RawMessage
		Cases    []struct {
			Name, Wire string
			Expected   map[string]json.RawMessage
		}
	}
	Enums []struct {
		Type    string
		Symbols map[string]int32
	}
	Rows []struct {
		Type, Name string
		Known      json.RawMessage
	}
}

func independentNativeABICorpus(t *testing.T, input map[string]any) independentABICorpus {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("independent protobufjs ABI requires pinned Bun/dependencies")
	}
	home := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, bun, "--no-env-file", nodeSourceFixturePath("node_native_proto_abi.fixture.ts"))
	child.Dir = home
	child.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_WHATSAPP_LEAF=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	input["homeDir"] = home
	child.Stdin = bytes.NewReader(nodeJSON(input))
	var stderr bytes.Buffer
	child.Stderr = &stderr
	data, err := child.Output()
	if err != nil {
		t.Fatalf("independent physical protobufjs ABI %v %.2500s", err, stderr.String())
	}
	var corpus independentABICorpus
	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatalf("independent ABI response invalid %v %.500s", err, data)
	}
	if corpus.Authority.Scope != "independent-pinned-native-proto-not-original-equivalence" || len(corpus.Authority.Schemas) < 30 {
		t.Fatal("native ABI fixture lacks independent schema authority", corpus.Authority)
	}
	for _, schema := range corpus.Authority.Schemas {
		if len(schema.SHA256) != 64 {
			t.Fatal("schema authority missing hash", schema.File)
		}
	}
	return corpus
}

func TestIndependentPinnedNativeProtocolPublicABIAbsentOriginalSchemas(t *testing.T) {
	var original []originalPropertyType
	originalPropertyCorpus(t, nil, &original)
	kinds, _, _ := propertyTypes(original)
	sharedTypes, sharedEnums := map[protoreflect.FullName]bool{}, map[protoreflect.FullName]bool{}
	for _, row := range original {
		kind := kinds[row.Type]
		if kind == nil {
			continue
		}
		sharedTypes[kind.Descriptor().FullName()] = true
		for name, schema := range row.FieldSchemas {
			if field := propertyField(kind.Descriptor(), name); field != nil && field.Enum() != nil && len(schema.Enum) > 0 {
				sharedEnums[field.Enum().FullName()] = true
			}
		}
	}
	nativeTypes, nativeEnums := []string{}, []string{}
	names := map[protoreflect.FullName]map[string]string{}
	protoregistry.GlobalTypes.RangeMessages(func(kind protoreflect.MessageType) bool {
		descriptor := kind.Descriptor()
		// This wire oracle loads the controlled WhatsApp public .proto tree.
		// Other process registries (Google/libsignal) have separate authorities.
		path := descriptor.ParentFile().Path()
		if !strings.HasPrefix(path, "wa") && !strings.HasPrefix(path, "instamadillo") {
			return true
		}
		if !sharedTypes[descriptor.FullName()] {
			nativeTypes = append(nativeTypes, string(descriptor.FullName()))
		}
		mapping := map[string]string{}
		for i := 0; i < descriptor.Fields().Len(); i++ {
			field := descriptor.Fields().Get(i)
			mapping[string(field.Name())] = string(field.Name())
			if oneof := field.ContainingOneof(); oneof != nil {
				mapping["selector:"+string(field.Name())] = string(oneof.Name())
			}
		}
		names[descriptor.FullName()] = mapping
		return true
	})
	protoregistry.GlobalTypes.RangeEnums(func(kind protoreflect.EnumType) bool {
		name := kind.Descriptor().FullName()
		path := kind.Descriptor().ParentFile().Path()
		if (strings.HasPrefix(path, "wa") || strings.HasPrefix(path, "instamadillo")) && !sharedEnums[name] {
			nativeEnums = append(nativeEnums, string(name))
		}
		return true
	})
	sort.Strings(nativeTypes)
	sort.Strings(nativeEnums)
	corpus := independentNativeABICorpus(t, map[string]any{"types": nativeTypes, "enums": nativeEnums})
	if len(corpus.Records) != len(nativeTypes) || len(corpus.Enums) != len(nativeEnums) {
		t.Fatal("independent public ABI inventory missing native constructors", len(corpus.Records), len(nativeTypes), len(corpus.Enums), len(nativeEnums))
	}
	for _, row := range corpus.Enums {
		kind, err := protoregistry.GlobalTypes.FindEnumByName(protoreflect.FullName(row.Type))
		if err != nil {
			t.Fatal(err)
		}
		t.Run("enum/"+row.Type, func(t *testing.T) { assertPublicABIEnum(t, kind, row.Symbols) })
	}
	forwarded := []map[string]string{}
	getters := 0
	for _, row := range corpus.Records {
		kind, err := protoregistry.GlobalTypes.FindMessageByName(protoreflect.FullName(row.Type))
		if err != nil {
			t.Fatal(err)
		}
		t.Run("message/"+row.Type, func(t *testing.T) {
			structure := reflect.TypeOf(kind.New().Interface())
			var independentDescriptor originalPropertyType
			if err := json.Unmarshal(nodeJSON(map[string]any{"type": row.Type, "fieldSchemas": row.Fields}), &independentDescriptor); err != nil {
				t.Fatal(err)
			}
			assertOriginalPublicSchemaDescriptor(t, kind.New().Interface(), independentDescriptor)
			for name, schema := range row.Fields {
				field := propertyField(kind.Descriptor(), name)
				if field == nil || int32(field.Number()) != schema.Tag {
					t.Fatal("independent schema tag missing/drifted", row.Type, name, schema.Tag)
				}
				method := generatedPropertyMethod(structure, field)
				if method == "" {
					t.Fatal("public getter absent for pinned native wire", row.Type, name)
				}
				for _, receiver := range []reflect.Value{reflect.Zero(structure), reflect.ValueOf(kind.New().Interface())} {
					got := reflectedPropertyJSON(receiver.MethodByName(method).Call(nil)[0], names)
					if !samePropertyJSON(got, row.Defaults[name]) {
						t.Errorf("nil/empty public getter default differs independent schema %s.%s Go=%.300s Node=%.300s", row.Type, name, nodeJSON(got), row.Defaults[name])
					}
				}
				getters++
			}
			for _, input := range row.Cases {
				wire, err := base64.StdEncoding.DecodeString(input.Wire)
				if err != nil {
					t.Fatal(err)
				}
				message := protoMessageFromWire(t, kind, wire)
				for name := range row.Fields {
					field := propertyField(kind.Descriptor(), name)
					method := generatedPropertyMethod(structure, field)
					expected, present := input.Expected[name]
					if !present {
						expected = row.Defaults[name]
					}
					got := reflectedPropertyJSON(reflect.ValueOf(message).MethodByName(method).Call(nil)[0], names)
					if !samePropertyJSON(got, expected) {
						t.Errorf("actual getter loses independent physical %s/%s/%s Go=%.500s Node=%.500s", row.Type, input.Name, name, nodeJSON(got), expected)
					}
					if field.HasPresence() && message.ProtoReflect().Has(field) != present {
						t.Error("independent wire presence lost", row.Type, input.Name, name, present)
					}
					if oneof := field.ContainingOneof(); oneof != nil && present && message.ProtoReflect().WhichOneof(oneof) != field {
						t.Error("independent oneof discriminator lost", row.Type, input.Name, name)
					}
				}
				forward := assertPublicABIMessageDebugReset(t, kind, wire)
				forwarded = append(forwarded, map[string]string{"type": row.Type, "name": input.Name, "wire": base64.StdEncoding.EncodeToString(forward), "expected": string(nodeJSON(input.Expected))})
			}
		})
	}
	if t.Failed() {
		return
	}
	received := independentNativeABICorpus(t, map[string]any{"wires": forwarded})
	if len(received.Rows) != len(forwarded) {
		t.Fatal("independent decoder cold ABI receipt missing", len(received.Rows), len(forwarded))
	}
	for i, row := range received.Rows {
		var actual any
		_ = json.Unmarshal(row.Known, &actual)
		if !samePropertyJSON(actual, []byte(forwarded[i]["expected"])) {
			t.Errorf("independent Node cold consumer lost physical native ABI %s/%s actual=%.500s expected=%.500s", row.Type, row.Name, row.Known, forwarded[i]["expected"])
		}
	}
	t.Logf("independent pinned schemas (not original parity): %d message types, %d getters, %d enum symbol tables, %d populated/oneof binary Node receipts", len(corpus.Records), getters, len(corpus.Enums), len(forwarded))
}
