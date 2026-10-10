package imruntime

import (
	"errors"
	"math"

	"github.com/NanmiCoder/cc-haha/backend/internal/imageprocessing"
	wa "github.com/NanmiCoder/cc-haha/backend/internal/whatsappcompat"
)

func WhatsAppImageThumbnail(codec imageprocessing.Codec, data []byte) (wa.NodeImageThumbnail, error) {
	metadata, e := codec.Metadata(data)
	if e != nil {
		return wa.NodeImageThumbnail{}, e
	}
	if metadata.Width <= 0 || metadata.Height <= 0 {
		return wa.NodeImageThumbnail{}, errors.New("image dimensions unavailable")
	}
	height := int(math.RoundToEven(float64(metadata.Height) * 32 / float64(metadata.Width)))
	if height < 1 {
		height = 1
	}
	encoder, ok := codec.(interface {
		EncodeThumbnail([]byte, int, int, int) ([]byte, error)
	})
	if !ok {
		return wa.NodeImageThumbnail{}, errors.New("native thumbnail codec unavailable")
	}
	jpeg, e := encoder.EncodeThumbnail(data, 32, height, 50)
	if e != nil {
		return wa.NodeImageThumbnail{}, e
	}
	return wa.NodeImageThumbnail{JPEG: jpeg, Width: uint32(metadata.Width), Height: uint32(metadata.Height)}, nil
}
