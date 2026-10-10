package imruntime

import (
	"fmt"
	"os"
)

func (r *FeishuRuntime) materializeOutbound(source UploadSource) (SafeImage, error) {
	switch source.Kind {
	case "base64":
		return SafeImage{OK: true, Buffer: nodeBase64(source.Data), MIME: source.MIME}, nil
	case "path":
		p, e := os.ReadFile(source.Path)
		if e != nil {
			return SafeImage{}, e
		}
		mime := source.MIME
		if mime == "" {
			mime = "image/png"
		}
		return SafeImage{OK: true, Buffer: p, MIME: mime}, nil
	default:
		loaded := FeishuLoadRemoteImage(r.ctx, source.URL, r.o.Remote)
		if !loaded.OK {
			return SafeImage{}, fmt.Errorf("%s", loaded.Reason)
		}
		return loaded, nil
	}
}
