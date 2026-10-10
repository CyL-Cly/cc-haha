package imregistration

// QRDataURL exposes the existing native QR display primitive for API consumers.
func QRDataURL(text string) (string, error) { return qrDataURL(text) }
