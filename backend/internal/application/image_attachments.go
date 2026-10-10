package application

import "github.com/NanmiCoder/cc-haha/backend/internal/imageprocessing"

func normalizeDesktopImage(data []byte, size int, extension string) (imageprocessing.Result, error) {
	codec, _ := imageprocessing.BundledCodec("")
	return imageprocessing.Normalize(codec, data, size, extension)
}
