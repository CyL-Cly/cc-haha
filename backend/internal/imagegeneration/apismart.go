package imagegeneration

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	weburl "github.com/nlnwa/whatwg-url/url"
)

func IsAPISmart(config Config) bool {
	if config.Kind != "openai_images" || config.BaseURL == "" {
		return false
	}
	u, err := weburl.Parse(config.BaseURL)
	return err == nil && u.Hostname() == "gw.apismart.ai"
}
func APISmartFields(input protocol.JSONObject, images []PreparedImage) (protocol.JSONObject, error) {
	if input.String("output_format") == "webp" {
		return protocol.JSONObject{}, errors.New("ApiSmart image output supports PNG or JPEG. Choose one of those formats.")
	}
	if input.String("background") == "transparent" {
		return protocol.JSONObject{}, errors.New("ApiSmart does not document transparent image backgrounds. Use an opaque background.")
	}
	for _, image := range images {
		if len(image.Bytes) > 10*1024*1024 {
			return protocol.JSONObject{}, errors.New("ApiSmart reference images must not exceed 10 MB each.")
		}
	}
	size := map[string]string{"1024x1024": "2048x2048", "1024x1536": "2048x3072", "1536x1024": "3072x2048"}[input.String("size")]
	if size == "" {
		ratio := input.String("aspect_ratio")
		if ratio == "" || ratio == "auto" {
			size = "2K"
		} else {
			parts := strings.Split(ratio, ":")
			if len(parts) < 2 {
				return protocol.JSONObject{}, errors.New("ApiSmart image aspect ratio must be between 1:16 and 16:1.")
			}
			width, _ := strconv.ParseFloat(parts[0], 64)
			height, _ := strconv.ParseFloat(parts[1], 64)
			if width == 0 || height == 0 || math.IsNaN(width) || math.IsNaN(height) || width/height < 1.0/16 || width/height > 16 {
				return protocol.JSONObject{}, errors.New("ApiSmart image aspect ratio must be between 1:16 and 16:1.")
			}
			scale := math.Sqrt(2048 * 2048 / (width * height))
			size = fmt.Sprintf("%dx%d", int64(math.Ceil(width*scale)), int64(math.Ceil(height*scale)))
		}
	}
	format := input.Get("output_format")
	if format == nil || string(format) == "null" {
		format = encoded("png")
	}
	return object(rawField("model", input.Get("model")), rawField("prompt", input.Get("prompt")), field("response_format", "url"), field("size", size), rawField("output_format", format)), nil
}
