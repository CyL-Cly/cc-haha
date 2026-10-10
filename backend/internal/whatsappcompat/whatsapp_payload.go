package whatsappcandidate

import (
	"context"
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"go.mau.fi/whatsmeow/proto/waCompanionReg"
	"go.mau.fi/whatsmeow/proto/waWa6"
	"go.mau.fi/whatsmeow/store"
	"google.golang.org/protobuf/proto"
)

const NodeWhatsAppVersionURL = "https://raw.githubusercontent.com/WhiskeySockets/Baileys/master/src/Defaults/index.ts"

var nodeWhatsAppFallbackVersion = [3]uint32{2, 3000, 1027934701}
var nodeVersionLine = regexp.MustCompile(`const version = \[(\d+),\s*(\d+),\s*(\d+)\]`)

func fetchNodeVersion(ctx context.Context, client *http.Client) [3]uint32 {
	request, e := http.NewRequestWithContext(ctx, "GET", NodeWhatsAppVersionURL, nil)
	if e != nil {
		return nodeWhatsAppFallbackVersion
	}
	response, e := client.Do(request)
	if e != nil {
		return nodeWhatsAppFallbackVersion
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nodeWhatsAppFallbackVersion
	}
	data, e := io.ReadAll(response.Body)
	if e != nil {
		return nodeWhatsAppFallbackVersion
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) < 7 {
		return nodeWhatsAppFallbackVersion
	}
	m := nodeVersionLine.FindStringSubmatch(lines[6])
	if len(m) != 4 {
		return nodeWhatsAppFallbackVersion
	}
	var version [3]uint32
	for i := 0; i < 3; i++ {
		v, e := strconv.ParseUint(m[i+1], 10, 32)
		if e != nil {
			return nodeWhatsAppFallbackVersion
		}
		version[i] = uint32(v)
	}
	return version
}

func NodeClientPayload(device *store.Device, version [3]uint32) *waWa6.ClientPayload {
	p := &waWa6.ClientPayload{UserAgent: &waWa6.ClientPayload_UserAgent{Platform: waWa6.ClientPayload_UserAgent_WEB.Enum(), ReleaseChannel: waWa6.ClientPayload_UserAgent_RELEASE.Enum(), AppVersion: &waWa6.ClientPayload_UserAgent_AppVersion{Primary: &version[0], Secondary: &version[1], Tertiary: &version[2]}, Mcc: proto.String("000"), Mnc: proto.String("000"), OsVersion: proto.String("0.1"), Device: proto.String("Desktop"), OsBuildNumber: proto.String("0.1"), LocaleLanguageIso6391: proto.String("en"), LocaleCountryIso31661Alpha2: proto.String("US")}, WebInfo: &waWa6.ClientPayload_WebInfo{WebSubPlatform: waWa6.ClientPayload_WebInfo_WEB_BROWSER.Enum()}, ConnectType: waWa6.ClientPayload_WIFI_UNKNOWN.Enum(), ConnectReason: waWa6.ClientPayload_USER_ACTIVATED.Enum()}
	if device.ID != nil {
		p.Username = proto.Uint64(device.ID.UserInt())
		p.Device = proto.Uint32(uint32(device.ID.Device))
		p.Passive = proto.Bool(true)
		p.Pull = proto.Bool(true)
		p.LidDbMigrated = proto.Bool(false)
		return p
	}
	props := proto.Clone(store.DeviceProps).(*waCompanionReg.DeviceProps)
	props.Os = proto.String("cc-haha")
	props.PlatformType = waCompanionReg.DeviceProps_DESKTOP.Enum()
	props.RequireFullSync = proto.Bool(false)
	props.Version = &waCompanionReg.DeviceProps_AppVersion{Primary: proto.Uint32(10), Secondary: proto.Uint32(15), Tertiary: proto.Uint32(7)}
	encoded, _ := proto.Marshal(props)
	reg := make([]byte, 4)
	binary.BigEndian.PutUint32(reg, device.RegistrationID)
	pre := make([]byte, 4)
	binary.BigEndian.PutUint32(pre, device.SignedPreKey.KeyID)
	hash := md5.Sum([]byte(fmt.Sprintf("%d.%d.%d", version[0], version[1], version[2])))
	p.Passive = proto.Bool(false)
	p.Pull = proto.Bool(false)
	p.DevicePairingData = &waWa6.ClientPayload_DevicePairingRegistrationData{ERegid: reg, EKeytype: []byte{5}, EIdent: device.IdentityKey.Pub[:], ESkeyID: pre[1:], ESkeyVal: device.SignedPreKey.Pub[:], ESkeySig: device.SignedPreKey.Signature[:], BuildHash: hash[:], DeviceProps: encoded}
	return p
}
