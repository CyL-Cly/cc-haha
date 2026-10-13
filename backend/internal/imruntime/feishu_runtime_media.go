package imruntime

import (
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/storagerelocations"
	"os"
)

func (r *FeishuRuntime) materializeOutbound(source UploadSource) (SafeImage, error) {
	switch source.Kind {
	case "base64":
		return SafeImage{OK: true, Buffer: nodeBase64(source.Data), MIME: source.MIME}, nil
	case "path":
		path := source.Path
		var e error
		if r.o.Config.ConfigDir != "" {
			path, e = storagerelocations.Resolve(path, r.o.Config.ConfigDir)
			if e != nil {
				return SafeImage{}, e
			}
		}
		p, e := os.ReadFile(path)
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
