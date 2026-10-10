//go:build darwin || linux || windows

package imageprocessing

import (
	"errors"
	"fmt"
	"math"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Vips invokes the same bundled native codec as the original image processor.
// GObject's non-variadic API avoids architecture-dependent C varargs. The
// library and its operation cache are shared; no session or authorization data
// is kept here.
type Vips struct {
	mu             sync.Mutex
	handle         uintptr
	init           func(string) int32
	errorBuffer    func() string
	errorClear     func()
	operation      func(string) uintptr
	build          func(*uintptr) int32
	unrefOutputs   func(uintptr)
	unref          func(uintptr)
	ref            func(uintptr) uintptr
	findLoader     func([]byte, uintptr) string
	blob           func(uintptr, *byte, uintptr) uintptr
	malloc         func(uintptr) *byte
	freeSymbol     uintptr
	areaUnref      func(uintptr)
	typeByName     func(string) uintptr
	valueInit      func(*nativeValue, uintptr) *nativeValue
	valueUnset     func(*nativeValue)
	valueSetObject func(*nativeValue, uintptr)
	valueSetBoxed  func(*nativeValue, uintptr)
	valueSetDouble func(*nativeValue, float64)
	valueGetObject func(*nativeValue) uintptr
	valueGetBlob   func(*nativeValue, *uintptr) *byte
	setProperty    func(uintptr, string, *nativeValue)
	getProperty    func(uintptr, string, *nativeValue)
	setString      func(uintptr, string, string) int32
	width          func(uintptr) int32
	height         func(uintptr) int32
	hasAlpha       func(uintptr) int32
	imageFormat    func(uintptr) int32
	interpretation func(uintptr) int32
	imageTypeof    func(uintptr, string) uintptr
}
type nativeValue struct {
	Type uintptr
	Data [2]uintptr
}

func OpenVips(path string) (*Vips, error) {
	handle, err := openVipsLibrary(path)
	if err != nil {
		return nil, err
	}
	p := &Vips{handle: handle}
	bindings := []struct {
		name     string
		function any
	}{
		{"vips_init", &p.init}, {"vips_error_buffer", &p.errorBuffer}, {"vips_error_clear", &p.errorClear},
		{"vips_operation_new", &p.operation}, {"vips_cache_operation_buildp", &p.build}, {"vips_object_unref_outputs", &p.unrefOutputs},
		{"g_object_unref", &p.unref}, {"g_object_ref", &p.ref}, {"vips_foreign_find_load_buffer", &p.findLoader},
		{"vips_blob_new", &p.blob}, {"vips_area_unref", &p.areaUnref}, {"g_type_from_name", &p.typeByName},
		{"g_malloc", &p.malloc},
		{"g_value_init", &p.valueInit}, {"g_value_unset", &p.valueUnset}, {"g_value_set_object", &p.valueSetObject},
		{"g_value_set_boxed", &p.valueSetBoxed}, {"g_value_get_object", &p.valueGetObject}, {"vips_value_get_blob", &p.valueGetBlob},
		{"g_value_set_double", &p.valueSetDouble},
		{"g_object_set_property", &p.setProperty}, {"g_object_get_property", &p.getProperty}, {"vips_object_set_argument_from_string", &p.setString},
		{"vips_image_get_width", &p.width}, {"vips_image_get_height", &p.height},
		{"vips_image_hasalpha", &p.hasAlpha}, {"vips_image_get_format", &p.imageFormat},
		{"vips_image_get_interpretation", &p.interpretation}, {"vips_image_get_typeof", &p.imageTypeof},
	}
	p.freeSymbol, err = lookupVipsSymbol(handle, "g_free")
	if err != nil {
		_ = closeVipsLibrary(handle)
		return nil, err
	}
	for _, binding := range bindings {
		symbol, err := lookupVipsSymbol(handle, binding.name)
		if err != nil {
			_ = closeVipsLibrary(handle)
			return nil, err
		}
		purego.RegisterFunc(binding.function, symbol)
	}
	if p.init("cc-haha-go-image") != 0 {
		err := p.failure()
		_ = closeVipsLibrary(handle)
		return nil, err
	}
	return p, nil
}
func (p *Vips) failure() error {
	message := strings.TrimSpace(p.errorBuffer())
	p.errorClear()
	if message == "" {
		message = "Native image operation failed"
	}
	return errors.New(message)
}
func (p *Vips) drop(operation uintptr) { p.unrefOutputs(operation); p.unref(operation) }
func (p *Vips) object(operation uintptr, name string, image uintptr) {
	var v nativeValue
	p.valueInit(&v, p.typeByName("VipsImage"))
	p.valueSetObject(&v, image)
	p.setProperty(operation, name, &v)
	p.valueUnset(&v)
}
func (p *Vips) double(operation uintptr, name string, value float64) {
	var v nativeValue
	p.valueInit(&v, 60)
	p.valueSetDouble(&v, value)
	p.setProperty(operation, name, &v)
	p.valueUnset(&v)
}
func (p *Vips) image(operation uintptr) uintptr {
	var v nativeValue
	p.valueInit(&v, p.typeByName("VipsImage"))
	p.getProperty(operation, "out", &v)
	out := p.valueGetObject(&v)
	if out != 0 {
		p.ref(out)
	}
	p.valueUnset(&v)
	return out
}
func (p *Vips) load(data []byte) (uintptr, string, error) {
	return p.loadWith(data, nil)
}
func (p *Vips) loadWith(data []byte, properties [][2]string) (uintptr, string, error) {
	if len(data) == 0 {
		return 0, "", errors.New("Image file is empty (0 bytes)")
	}
	loader := p.findLoader(data, uintptr(len(data)))
	if loader == "" {
		return 0, "", p.failure()
	}
	operation := p.operation(loader)
	if operation == 0 {
		return 0, "", p.failure()
	}
	defer func() { p.drop(operation) }()
	// An operation can survive in libvips's process cache. Never retain a Go
	// slice there: the blob owns a C allocation and releases it with g_free.
	memory := p.malloc(uintptr(len(data)))
	if memory == nil {
		return 0, "", errors.New("Native image allocation failed")
	}
	copy(unsafe.Slice(memory, len(data)), data)
	blob := p.blob(p.freeSymbol, memory, uintptr(len(data)))
	if blob == 0 {
		return 0, "", p.failure()
	}
	var v nativeValue
	p.valueInit(&v, p.typeByName("VipsBlob"))
	p.valueSetBoxed(&v, blob)
	p.setProperty(operation, "buffer", &v)
	p.valueUnset(&v)
	p.areaUnref(blob)
	if p.setString(operation, "access", "sequential") != 0 || p.setString(operation, "fail_on", "warning") != 0 {
		return 0, "", p.failure()
	}
	for _, property := range properties {
		if p.setString(operation, property[0], property[1]) != 0 {
			return 0, "", p.failure()
		}
	}
	if p.build(&operation) != 0 {
		return 0, "", p.failure()
	}
	out := p.image(operation)
	if out == 0 {
		return 0, "", p.failure()
	}
	format := ""
	for _, candidate := range []string{"png", "jpeg", "gif", "webp", "tiff", "svg", "heif", "jp2", "jxl", "pdf"} {
		if strings.Contains(strings.ToLower(loader), candidate) {
			format = candidate
			break
		}
	}
	return out, format, nil
}
func (p *Vips) Metadata(data []byte) (Metadata, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	defer runtime.KeepAlive(data)
	image, format, err := p.load(data)
	if err != nil {
		return Metadata{}, err
	}
	defer p.unref(image)
	return Metadata{Width: int(p.width(image)), Height: int(p.height(image)), Format: format}, nil
}
func (p *Vips) unary(name string, input uintptr, properties ...[2]string) (uintptr, error) {
	operation := p.operation(name)
	if operation == 0 {
		return 0, p.failure()
	}
	defer func() { p.drop(operation) }()
	p.object(operation, "in", input)
	for _, property := range properties {
		if p.setString(operation, property[0], property[1]) != 0 {
			return 0, p.failure()
		}
	}
	if p.build(&operation) != 0 {
		return 0, p.failure()
	}
	output := p.image(operation)
	if output == 0 {
		return 0, p.failure()
	}
	return output, nil
}
func (p *Vips) Encode(data []byte, width, height int, format string, quality int) ([]byte, error) {
	if format == "webp" {
		// Normalize's lossless WebP stage uses Sharp's default quality; the
		// requested JPEG quality does not change that compression profile.
		quality = 80
	}
	return p.EncodeConfigured(data, width, height, Encoding{Format: format, Quality: quality, Lossless: format == "webp"})
}

// Sharp's processing ICC stage precedes resize, and its final colourspace
// stage converts grey/16-bit inputs to the default sRGB output. The actual
// bundled libvips profiles perform these conversions; pixels are not recreated.
func (p *Vips) processingProfile(input uintptr) (uintptr, error) {
	// VipsInterpretation enum values are part of the bundled native ABI.
	const cmyk, labs, rgb16, grey16 = 15, 21, 25, 26
	interpretation := p.interpretation(input)
	profile, depth := "srgb", "8"
	if interpretation == rgb16 {
		profile, depth = "p3", "16"
	}
	if p.imageTypeof(input, "icc-profile-data") != 0 && interpretation != labs && interpretation != grey16 {
		converted, err := p.unary("icc_transform", input, [2]string{"output_profile", profile}, [2]string{"embedded", "true"}, [2]string{"depth", depth}, [2]string{"intent", "perceptual"})
		if err != nil {
			// Original Sharp catches invalid embedded profiles and continues with
			// the loaded pixels; failure() has already cleared the native error.
			return 0, nil
		}
		return converted, nil
	}
	if interpretation == cmyk {
		return p.unary("icc_transform", input, [2]string{"output_profile", profile}, [2]string{"input_profile", "cmyk"}, [2]string{"intent", "perceptual"})
	}
	return 0, nil
}

func (p *Vips) EncodeConfigured(data []byte, width, height int, profile Encoding) ([]byte, error) {
	return p.encodeConfigured(data, width, height, profile, false)
}

// EncodeThumbnail keeps Sharp resize(width) enlargement enabled.
func (p *Vips) EncodeThumbnail(data []byte, width, height, quality int) ([]byte, error) {
	return p.encodeConfigured(data, width, height, Encoding{Format: "jpeg", Quality: quality}, true)
}

func (p *Vips) encodeConfigured(data []byte, width, height int, profile Encoding, enlarge bool) ([]byte, error) {
	format, quality := profile.Format, profile.Quality
	p.mu.Lock()
	defer p.mu.Unlock()
	defer runtime.KeepAlive(data)
	image, inputFormat, err := p.load(data)
	if err != nil {
		return nil, err
	}
	defer p.unref(image)
	current := image
	if width > 0 && height > 0 && (width != int(p.width(image)) || height != int(p.height(image))) {
		shrink := math.Max(float64(p.width(image))/float64(width), float64(p.height(image))/float64(height))
		if enlarge {
			shrink = math.Min(float64(p.width(image))/float64(width), float64(p.height(image)))
		}
		shrink = math.Max(1, shrink)
		var preload [][2]string
		if inputFormat == "jpeg" {
			factor := 1
			if shrink >= 8 {
				factor = 8
			} else if shrink >= 4 {
				factor = 4
			} else if shrink >= 2 {
				factor = 2
			}
			if factor > 1 && int(shrink) == factor {
				factor /= 2
			}
			if factor > 1 {
				preload = [][2]string{{"shrink", fmt.Sprint(factor)}}
			}
		} else if (inputFormat == "webp" || inputFormat == "svg" || inputFormat == "pdf") && shrink > 1 {
			preload = [][2]string{{"scale", fmt.Sprintf("%.17g", 1/shrink)}}
		}
		if len(preload) > 0 {
			current, _, err = p.loadWith(data, preload)
			if err != nil {
				return nil, err
			}
			defer p.unref(current)
		}
		processed, err := p.processingProfile(current)
		if err != nil {
			return nil, err
		}
		if processed != 0 {
			current = processed
			defer p.unref(current)
		}
		if !enlarge || int(p.width(current)) != width || int(p.height(current)) != height {
			alpha := p.hasAlpha(image) != 0
			formats := []string{"uchar", "char", "ushort", "short", "uint", "int", "float", "complex", "double", "dpcomplex"}
			originalFormat := int(p.imageFormat(image))
			if originalFormat < 0 || originalFormat >= len(formats) {
				return nil, errors.New("Unsupported image band format")
			}
			if alpha {
				premultiplied, err := p.unary("premultiply", current)
				if err != nil {
					return nil, err
				}
				defer p.unref(premultiplied)
				current, err = p.unary("cast", premultiplied, [2]string{"format", formats[originalFormat]})
				if err != nil {
					return nil, err
				}
				defer p.unref(current)
			}
			resize := p.operation("resize")
			if resize == 0 {
				return nil, p.failure()
			}
			defer func() { p.drop(resize) }()
			p.object(resize, "in", current)
			shrinkScale := math.Max(float64(p.width(current))/float64(width), float64(p.height(current))/float64(height))
			if enlarge {
				shrinkScale = float64(p.width(current)) / float64(width)
			}
			if !enlarge {
				shrinkScale = math.Max(1, shrinkScale)
			}
			scale := 1 / shrinkScale
			vscale := scale
			if enlarge {
				vscale = 1 / math.Min(shrinkScale, float64(p.height(current)))
			}
			p.double(resize, "scale", scale)
			p.double(resize, "vscale", vscale)
			if p.setString(resize, "kernel", "lanczos3") != 0 {
				return nil, p.failure()
			}
			if p.build(&resize) != 0 {
				return nil, p.failure()
			}
			current = p.image(resize)
			if current == 0 {
				return nil, p.failure()
			}
			defer p.unref(current)
			if alpha {
				unpremultiplied, err := p.unary("unpremultiply", current)
				if err != nil {
					return nil, err
				}
				defer p.unref(unpremultiplied)
				current, err = p.unary("cast", unpremultiplied, [2]string{"format", formats[originalFormat]})
				if err != nil {
					return nil, err
				}
				defer p.unref(current)
			}
		}
	} else {
		processed, err := p.processingProfile(current)
		if err != nil {
			return nil, err
		}
		if processed != 0 {
			current = processed
			defer p.unref(current)
		}
	}
	interpretation := p.interpretation(current)
	if interpretation == 25 || interpretation == 26 {
		current, err = p.unary("cast", current, [2]string{"format", "ushort"})
		if err != nil {
			return nil, err
		}
		defer p.unref(current)
	}
	if interpretation != 22 {
		properties := [][2]string{{"space", "srgb"}}
		if source := map[int32]string{1: "b-w", 15: "cmyk", 17: "rgb", 25: "rgb16", 26: "grey16"}[interpretation]; source != "" {
			properties = append(properties, [2]string{"source_space", source})
		}
		current, err = p.unary("colourspace", current, properties...)
		if err != nil {
			return nil, err
		}
		defer p.unref(current)
	}
	operation := p.operation(format + "save_buffer")
	if operation == 0 {
		return nil, p.failure()
	}
	defer func() { p.drop(operation) }()
	p.object(operation, "in", current)
	properties := [][2]string{{"keep", "none"}}
	switch format {
	case "png":
		palette := "false"
		if profile.Palette {
			palette = "true"
		}
		bitdepth := 8
		if profile.Colors > 0 {
			bitdepth = 1 << int(math.Floor(math.Log2(math.Ceil(math.Log2(float64(profile.Colors))))))
		}
		properties = append(properties, [2]string{"compression", "9"}, [2]string{"filter", "none"}, [2]string{"palette", palette}, [2]string{"bitdepth", fmt.Sprint(bitdepth)}, [2]string{"Q", "100"}, [2]string{"effort", "7"}, [2]string{"dither", "1"})
	case "webp":
		lossless := "false"
		if profile.Lossless {
			lossless = "true"
		}
		if quality == 0 {
			quality = 80
		}
		properties = append(properties, [2]string{"lossless", lossless}, [2]string{"Q", fmt.Sprint(quality)}, [2]string{"effort", "4"}, [2]string{"near_lossless", "false"}, [2]string{"smart_subsample", "false"}, [2]string{"smart_deblock", "false"}, [2]string{"preset", "default"}, [2]string{"min_size", "false"}, [2]string{"mixed", "false"}, [2]string{"alpha_q", "100"})
	case "jpeg":
		properties = append(properties, [2]string{"Q", fmt.Sprint(quality)}, [2]string{"subsample_mode", "on"}, [2]string{"optimize_coding", "true"})
	default:
		return nil, errors.New("Unsupported native image encoding")
	}
	for _, property := range properties {
		if p.setString(operation, property[0], property[1]) != 0 {
			return nil, p.failure()
		}
	}
	if p.build(&operation) != 0 {
		return nil, p.failure()
	}
	var v nativeValue
	p.valueInit(&v, p.typeByName("VipsBlob"))
	defer p.valueUnset(&v)
	p.getProperty(operation, "buffer", &v)
	var size uintptr
	pointer := p.valueGetBlob(&v, &size)
	if pointer == nil || size == 0 {
		return nil, p.failure()
	}
	return append([]byte(nil), unsafe.Slice(pointer, int(size))...), nil
}
