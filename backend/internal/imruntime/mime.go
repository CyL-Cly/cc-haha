package imruntime

import "strings"

var filenameMIME = map[string]string{"png": "image/png", "jpg": "image/jpeg", "jpeg": "image/jpeg", "gif": "image/gif", "webp": "image/webp", "bmp": "image/bmp", "svg": "image/svg+xml", "heic": "image/heic", "pdf": "application/pdf", "txt": "text/plain", "md": "text/markdown", "csv": "text/csv", "json": "application/json", "xml": "application/xml", "yaml": "application/yaml", "yml": "application/yaml", "zip": "application/zip", "gz": "application/gzip", "tar": "application/x-tar", "doc": "application/msword", "docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "xls": "application/vnd.ms-excel", "xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "ppt": "application/vnd.ms-powerpoint", "pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation", "mp3": "audio/mpeg", "m4a": "audio/mp4", "wav": "audio/wav", "ogg": "audio/ogg", "mp4": "video/mp4", "mov": "video/quicktime"}

func InferMIMEFromFilename(name string) string {
	parts := strings.Split(strings.ToLower(name), ".")
	if len(parts) < 2 {
		return ""
	}
	return filenameMIME[parts[len(parts)-1]]
}
func ImageExtensionForMIME(mime string) string {
	mime = strings.ToLower(strings.TrimSpace(strings.Split(mime, ";")[0]))
	for _, pair := range [][2]string{{"image/png", "png"}, {"image/jpeg", "jpg"}, {"image/gif", "gif"}, {"image/webp", "webp"}, {"image/bmp", "bmp"}, {"image/svg+xml", "svg"}, {"image/heic", "heic"}} {
		if pair[0] == mime {
			return pair[1]
		}
	}
	return "png"
}
