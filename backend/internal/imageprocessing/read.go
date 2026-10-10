package imageprocessing

import (
	"fmt"
	"math"
)

func ReadImage(codec Codec, data []byte, path string, maxTokens float64) (Result, error) {
	if len(data) == 0 {
		return Result{}, fmt.Errorf("Image file is empty: %s", path)
	}
	extension := DetectFormat(data)
	output, err := Normalize(codec, data, len(data), extension)
	if err != nil {
		return Result{}, err
	}
	if dims := output.Dimensions; dims != nil && dims.DisplayWidth > 0 && dims.DisplayHeight > 0 && math.Ceil(float64(dims.DisplayWidth)*float64(dims.DisplayHeight)/750) > maxTokens {
		sampled, err := Downsample(codec, data, len(data), extension, maxTokens)
		if err == nil {
			return sampled, nil
		}
		compressed, err := Compress(codec, data, int(math.Floor(math.Floor(maxTokens/.125)*.75)), "image/"+extension)
		if err == nil {
			return compressed, nil
		}
		if codec != nil {
			encoded, err := codec.Encode(data, 400, 400, "jpeg", 20)
			if err == nil {
				return Result{Buffer: encoded, MediaType: "jpeg"}, nil
			}
		}
		return Result{Buffer: data, MediaType: extension}, nil
	}
	return output, nil
}

func Compress(codec Codec, data []byte, maxBytes int, originalType string) (Result, error) {
	output, err := compress(codec, data, maxBytes, originalType)
	if err == nil {
		return output, nil
	}
	if len(data) <= maxBytes {
		return Result{Buffer: data, MediaType: DetectFormat(data)}, nil
	}
	return Result{}, &ResizeError{fmt.Sprintf("Unable to compress image (%s) to fit within %s. Please use a smaller image.", FormatSize(len(data)), FormatSize(maxBytes))}
}
func compress(codec Codec, data []byte, maxBytes int, originalType string) (Result, error) {
	if codec == nil {
		return Result{}, fmt.Errorf("Native image processor module not available")
	}
	metadata, err := codec.Metadata(data)
	if err != nil {
		return Result{}, err
	}
	format := metadata.Format
	if format == "" {
		format = originalType
		if len(format) >= 6 && format[:6] == "image/" {
			format = format[6:]
		}
		if format == "" {
			format = "jpeg"
		}
	}
	if format == "jpg" {
		format = "jpeg"
	}
	if len(data) <= maxBytes {
		return Result{Buffer: data, MediaType: format}, nil
	}
	advanced, ok := codec.(AdvancedCodec)
	if !ok {
		return Result{}, fmt.Errorf("Native compatibility image encoding profiles are not bound")
	}
	for _, scale := range []float64{1, .75, .5, .25} {
		width, height := metadata.Width, metadata.Height
		if width == 0 {
			width = 2000
		}
		if height == 0 {
			height = 2000
		}
		width = int(math.Floor(float64(width)*scale + .5))
		height = int(math.Floor(float64(height)*scale + .5))
		profile := Encoding{Format: format, Quality: 80, Palette: format == "png"}
		encoded, err := advanced.EncodeConfigured(data, width, height, profile)
		if err != nil {
			return Result{}, err
		}
		if len(encoded) <= maxBytes {
			return Result{Buffer: encoded, MediaType: format}, nil
		}
	}
	if format == "png" {
		encoded, err := advanced.EncodeConfigured(data, 800, 800, Encoding{Format: "png", Palette: true, Colors: 64})
		if err != nil {
			return Result{}, err
		}
		if len(encoded) <= maxBytes {
			return Result{Buffer: encoded, MediaType: "png"}, nil
		}
	}
	encoded, err := codec.Encode(data, 600, 600, "jpeg", 50)
	if err != nil {
		return Result{}, err
	}
	if len(encoded) <= maxBytes {
		return Result{Buffer: encoded, MediaType: "jpeg"}, nil
	}
	encoded, err = codec.Encode(data, 400, 400, "jpeg", 20)
	return Result{Buffer: encoded, MediaType: "jpeg"}, err
}
