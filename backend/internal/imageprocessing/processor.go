// Package imageprocessing preserves the original image normalization strategy.
// Its codec interface operates only on immutable pixels and dimensions.
package imageprocessing

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
)

type Metadata struct {
	Width, Height int
	Format        string
}
type Codec interface {
	Metadata([]byte) (Metadata, error)
	Encode([]byte, int, int, string, int) ([]byte, error)
}
type Encoding struct {
	Format   string
	Quality  int
	Palette  bool
	Colors   int
	Lossless bool
}
type AdvancedCodec interface {
	Codec
	EncodeConfigured([]byte, int, int, Encoding) ([]byte, error)
}

const TargetRawSize = 5 * 1024 * 1024 * 3 / 4
const MaximumDimension = 8000

type Dimensions struct {
	OriginalWidth  int `json:"originalWidth,omitempty"`
	OriginalHeight int `json:"originalHeight,omitempty"`
	DisplayWidth   int `json:"displayWidth,omitempty"`
	DisplayHeight  int `json:"displayHeight,omitempty"`
}
type Result struct {
	Buffer     []byte
	MediaType  string
	Dimensions *Dimensions
}
type ResizeError struct{ Message string }

func (e *ResizeError) Error() string { return e.Message }
func DetectFormat(data []byte) string {
	if len(data) < 4 {
		return "png"
	}
	if string(data[:4]) == string([]byte{0x89, 'P', 'N', 'G'}) {
		return "png"
	}
	if data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff {
		return "jpeg"
	}
	if string(data[:3]) == "GIF" {
		return "gif"
	}
	if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return "webp"
	}
	return "png"
}
func constrain(width, height, maxWidth, maxHeight int) (int, int) {
	if width > maxWidth {
		height = int(math.Floor(float64(height)*float64(maxWidth)/float64(width) + .5))
		width = maxWidth
	}
	if height > maxHeight {
		width = int(math.Floor(float64(width)*float64(maxHeight)/float64(height) + .5))
		height = maxHeight
	}
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	return width, height
}
func result(data []byte, format string, metadata Metadata, width, height int) Result {
	return Result{data, format, &Dimensions{metadata.Width, metadata.Height, width, height}}
}
func candidate(codec Codec, data []byte, metadata Metadata, width, height int, maxBytes int, fallback bool) (*Result, error) {
	format := metadata.Format
	if format == "jpg" {
		format = "jpeg"
	}
	formats := []struct {
		name    string
		quality int
	}{}
	if format == "png" {
		formats = append(formats, struct {
			name    string
			quality int
		}{"png", 0}, struct {
			name    string
			quality int
		}{"webp", 0})
	} else if format == "webp" {
		formats = append(formats, struct {
			name    string
			quality int
		}{"webp", 0})
	}
	for _, quality := range []int{85, 75} {
		formats = append(formats, struct {
			name    string
			quality int
		}{"jpeg", quality})
	}
	if fallback {
		formats = append(formats, struct {
			name    string
			quality int
		}{"jpeg", 65})
	}
	for _, profile := range formats {
		encoded, err := codec.Encode(data, width, height, profile.name, profile.quality)
		if err != nil {
			return nil, err
		}
		if len(encoded) <= maxBytes {
			output := result(encoded, profile.name, metadata, width, height)
			return &output, nil
		}
	}
	return nil, nil
}
func Normalize(codec Codec, data []byte, originalSize int, extension string) (Result, error) {
	if len(data) == 0 {
		return Result{}, &ResizeError{"Image file is empty (0 bytes)"}
	}
	output, err := normalize(codec, data, originalSize, extension)
	if err == nil {
		return output, nil
	}
	// Source's native-module/decoder failure fallback is determined by actual
	// bytes, including the PNG IHDR cap. It never trusts the filename extension.
	base64Size := int(math.Ceil(float64(originalSize) * 4 / 3))
	overDimension := len(data) >= 24 && string(data[:4]) == string([]byte{0x89, 'P', 'N', 'G'}) && (binary.BigEndian.Uint32(data[16:20]) > MaximumDimension || binary.BigEndian.Uint32(data[20:24]) > MaximumDimension)
	if base64Size <= 5*1024*1024 && !overDimension {
		return Result{Buffer: data, MediaType: DetectFormat(data)}, nil
	}
	if overDimension {
		return Result{}, &ResizeError{fmt.Sprintf("Unable to resize image — dimensions exceed the %dx%dpx limit and image processing failed. Please resize the image to reduce its pixel dimensions.", MaximumDimension, MaximumDimension)}
	}
	return Result{}, &ResizeError{fmt.Sprintf("Unable to resize image (%s raw, %s base64). The image exceeds the 5MB API limit and compression failed. Please resize the image manually or use a smaller image.", FormatSize(originalSize), FormatSize(base64Size))}
}
func normalize(codec Codec, data []byte, originalSize int, extension string) (Result, error) {
	if codec == nil {
		return Result{}, errors.New("Native image processor module not available")
	}
	metadata, err := codec.Metadata(data)
	if err != nil {
		return Result{}, err
	}
	format := metadata.Format
	if format == "" {
		format = extension
	}
	if format == "jpg" {
		format = "jpeg"
	}
	metadata.Format = format
	if metadata.Width == 0 || metadata.Height == 0 {
		if originalSize > TargetRawSize {
			encoded, err := codec.Encode(data, 0, 0, "jpeg", 80)
			return Result{Buffer: encoded, MediaType: "jpeg"}, err
		}
		return Result{Buffer: data, MediaType: format}, nil
	}
	width, height := metadata.Width, metadata.Height
	if originalSize <= TargetRawSize && width <= MaximumDimension && height <= MaximumDimension {
		return result(data, format, metadata, width, height), nil
	}
	hardWidth, hardHeight := constrain(width, height, MaximumDimension, MaximumDimension)
	resized := hardWidth != width || hardHeight != height
	if !resized && originalSize > TargetRawSize {
		output, err := candidate(codec, data, metadata, width, height, TargetRawSize, false)
		if err != nil {
			return Result{}, err
		}
		if output != nil {
			return *output, nil
		}
	}
	if resized {
		output, err := candidate(codec, data, metadata, hardWidth, hardHeight, TargetRawSize, false)
		if err != nil {
			return Result{}, err
		}
		if output != nil {
			return *output, nil
		}
	}
	softWidth, softHeight := constrain(hardWidth, hardHeight, 2048, 2048)
	if softWidth != hardWidth || softHeight != hardHeight {
		output, err := candidate(codec, data, metadata, softWidth, softHeight, TargetRawSize, false)
		if err != nil {
			return Result{}, err
		}
		if output != nil {
			return *output, nil
		}
	}
	readableWidth, readableHeight := constrain(hardWidth, hardHeight, 1568, 1568)
	if readableWidth != softWidth || readableHeight != softHeight {
		output, err := candidate(codec, data, metadata, readableWidth, readableHeight, TargetRawSize, true)
		if err != nil {
			return Result{}, err
		}
		if output != nil {
			return *output, nil
		}
	}
	return Result{}, &ResizeError{fmt.Sprintf("Unable to compress image (%s) below the 5MB API base64 limit without over-compressing it. Please resize the image manually or use a smaller image.", FormatSize(originalSize))}
}
func Downsample(codec Codec, data []byte, originalSize int, extension string, maxTokens float64) (Result, error) {
	if codec == nil {
		return Result{}, errors.New("Native image processor module not available")
	}
	metadata, err := codec.Metadata(data)
	if err != nil {
		return Result{}, err
	}
	if metadata.Format == "" {
		metadata.Format = extension
	}
	if metadata.Format == "jpg" {
		metadata.Format = "jpeg"
	}
	if metadata.Width == 0 || metadata.Height == 0 {
		return Result{}, &ResizeError{"Unable to downsample image for token budget because dimensions are unavailable."}
	}
	width, height := constrain(metadata.Width, metadata.Height, MaximumDimension, MaximumDimension)
	pixels := math.Max(1, maxTokens*750)
	if float64(width)*float64(height) > pixels {
		scale := math.Sqrt(pixels / (float64(width) * float64(height)))
		width = int(math.Max(1, math.Floor(float64(width)*scale+.5)))
		height = int(math.Max(1, math.Floor(float64(height)*scale+.5)))
		for float64(width)*float64(height) > pixels {
			if width >= height {
				width--
			} else {
				height--
			}
		}
	}
	output, err := candidate(codec, data, metadata, width, height, TargetRawSize, true)
	if err != nil {
		return Result{}, err
	}
	if output != nil {
		return *output, nil
	}
	readableWidth, readableHeight := constrain(width, height, 1568, 1568)
	if width != readableWidth || height != readableHeight {
		output, err = candidate(codec, data, metadata, readableWidth, readableHeight, TargetRawSize, true)
		if err != nil {
			return Result{}, err
		}
		if output != nil {
			return *output, nil
		}
	}
	return Result{}, &ResizeError{fmt.Sprintf("Unable to downsample image (%s) below the %g token budget without over-compressing it.", FormatSize(originalSize), maxTokens)}
}
func MetadataText(dimensions *Dimensions, path string) string {
	if dimensions == nil || dimensions.OriginalWidth == 0 || dimensions.OriginalHeight == 0 || dimensions.DisplayWidth <= 0 || dimensions.DisplayHeight <= 0 {
		if path != "" {
			return "[Image source: " + path + "]"
		}
		return ""
	}
	resized := dimensions.OriginalWidth != dimensions.DisplayWidth || dimensions.OriginalHeight != dimensions.DisplayHeight
	if !resized && path == "" {
		return ""
	}
	parts := []string{}
	if path != "" {
		parts = append(parts, "source: "+path)
	}
	if resized {
		parts = append(parts, fmt.Sprintf("original %dx%d, displayed at %dx%d. Multiply coordinates by %.2f to map to original image.", dimensions.OriginalWidth, dimensions.OriginalHeight, dimensions.DisplayWidth, dimensions.DisplayHeight, float64(dimensions.OriginalWidth)/float64(dimensions.DisplayWidth)))
	}
	return "[Image: " + strings.Join(parts, ", ") + "]"
}
func FormatSize(size int) string {
	units := []string{"bytes", "KB", "MB", "GB"}
	index := 0
	value := float64(size)
	for value >= 1024 && index < len(units)-1 {
		value /= 1024
		index++
	}
	if index == 0 {
		return fmt.Sprintf("%d bytes", size)
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", value), ".0") + units[index]
}
