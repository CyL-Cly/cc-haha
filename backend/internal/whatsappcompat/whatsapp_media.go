package whatsappcandidate

import (
	"context"
	"errors"
	"strings"

	"go.mau.fi/libsignal/session"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

func (n *NodeWhatsAppClient) SendMedia(ctx context.Context, jid types.JID, data []byte, mime, caption, name string) (whatsmeow.SendResponse, error) {
	typeName := whatsmeow.MediaDocument
	if strings.HasPrefix(mime, "image/") {
		typeName = whatsmeow.MediaImage
	} else if strings.HasPrefix(mime, "video/") {
		typeName = whatsmeow.MediaVideo
	} else if strings.HasPrefix(mime, "audio/") {
		typeName = whatsmeow.MediaAudio
		if mime == "audio/ogg" {
			mime = "audio/ogg; codecs=opus"
		}
	}
	u, e := n.Client.Upload(ctx, data, typeName)
	if e != nil {
		return whatsmeow.SendResponse{}, e
	}
	message := &waE2E.Message{}
	switch typeName {
	case whatsmeow.MediaImage:
		m := &waE2E.ImageMessage{URL: &u.URL, DirectPath: &u.DirectPath, MediaKey: u.MediaKey, FileEncSHA256: u.FileEncSHA256, FileSHA256: u.FileSHA256, FileLength: &u.FileLength, Mimetype: proto.String(mime), MediaKeyTimestamp: proto.Int64(n.now().Unix())}
		m.Caption = proto.String(caption)
		if n.imageThumbnail != nil {
			if info, e := n.imageThumbnail(data); e == nil {
				m.JPEGThumbnail = info.JPEG
				m.Width = proto.Uint32(info.Width)
				m.Height = proto.Uint32(info.Height)
			} else {
				n.Client.Log.Warnf("failed to obtain extra info: %v", e)
			}
		}
		message.ImageMessage = m
	case whatsmeow.MediaVideo:
		m := &waE2E.VideoMessage{URL: &u.URL, DirectPath: &u.DirectPath, MediaKey: u.MediaKey, FileEncSHA256: u.FileEncSHA256, FileSHA256: u.FileSHA256, FileLength: &u.FileLength, Mimetype: proto.String(mime), MediaKeyTimestamp: proto.Int64(n.now().Unix())}
		m.Caption = proto.String(caption)
		if thumb, e := nodeVideoThumbnail(ctx, data); e == nil {
			m.JPEGThumbnail = thumb
		} else {
			n.Client.Log.Debugf("could not generate video thumb: %v", e)
		}
		message.VideoMessage = m
	case whatsmeow.MediaAudio:
		message.AudioMessage = &waE2E.AudioMessage{URL: &u.URL, DirectPath: &u.DirectPath, MediaKey: u.MediaKey, FileEncSHA256: u.FileEncSHA256, FileSHA256: u.FileSHA256, FileLength: &u.FileLength, Mimetype: proto.String(mime), PTT: proto.Bool(true), MediaKeyTimestamp: proto.Int64(n.now().Unix())}
		if seconds, e := NodeAudioDuration(data); e == nil {
			message.AudioMessage.Seconds = proto.Uint32(seconds)
		} else if !errors.Is(e, errNodeAudioDurationUnavailable) {
			n.Client.Log.Warnf("failed to obtain extra info: %v", e)
		}
	default:
		if name == "" {
			name = "file"
		}
		if mime == "" {
			mime = "application/octet-stream"
		}
		m := &waE2E.DocumentMessage{URL: &u.URL, DirectPath: &u.DirectPath, MediaKey: u.MediaKey, FileEncSHA256: u.FileEncSHA256, FileSHA256: u.FileSHA256, FileLength: &u.FileLength, Mimetype: proto.String(mime), FileName: proto.String(name), MediaKeyTimestamp: proto.Int64(n.now().Unix())}
		m.Caption = proto.String(caption)
		message.DocumentMessage = m
	}
	return n.Client.SendMessage(session.WithNodeSessionTransactions(ctx, n.Store.AcquireSessions), jid, message)
}
