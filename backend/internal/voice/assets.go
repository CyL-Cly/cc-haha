package voice

import (
	"fmt"
	"path/filepath"
	"strings"
)

const SherpaVersion = "1.13.8"

type DownloadHash struct {
	Algorithm string `json:"algorithm"`
	Encoding  string `json:"encoding"`
	Value     string `json:"value"`
}
type Asset struct {
	Name  string       `json:"name"`
	Bytes int64        `json:"bytes"`
	Hash  DownloadHash `json:"hash"`
	URLs  []string     `json:"urls"`
}
type Item struct {
	Step   string `json:"step"`
	Kind   string `json:"kind"`
	Asset  Asset  `json:"asset"`
	Target string `json:"target"`
	Marker string `json:"marker,omitempty"`
}
type runtimeAsset struct {
	Bytes     int64
	Integrity string
}

var runtimes = map[string]runtimeAsset{
	"sherpa-onnx-node": {11954, "MsDMBdhLFTZ1GwvcGSSQhnS7g/EA8OMH6IYysCVUOM7j8Icty9KRc0E6YT1A5fWBsZwRfKOeh88QC95aRvS8ag=="},
	"darwin-arm64":     {10047754, "FPNgJMgnWVl/KhRTIhG3KL3A4Om63Rn4YKXc9/uHY7SzLcvqLJLc/h7UBWJwduXvv7K18t5NpxHR6XgXn4sjWw=="},
	"darwin-x64":       {11191481, "7BLRpjM6w4f9W46/nmkmq8lEKUayhebvcpslCVQ+6QN2uReYlZEMDZlSpXMjme+hUFrPfRz8P3UNq8ep/4d19g=="},
	"linux-x64":        {11089653, "6plnhjagsSeTntCgnlag86hWbs/uZE9Crms1LgOb68/1nKsIQjMd+WG519m+aPwT6TrsBOiEMzrx41t8sL5L5g=="},
	"linux-arm64":      {13910679, "Tlg7a70b/Wge3OF8IgTHF9jhSVCsLyKQKhwc4BsJ5A+dL/SrFtGBjzuHp4XeLhiiOT7afCxX5PdSn/D4c8Lnuw=="},
	"win-x64":          {8894875, "oZF1c9VPOKtMwn83Bboc5XSWL+76BRoyB3eUuVnCknBKxwSULZU2Foia9VHWzU+n4I12rPsP6z6H9Rp1hD9o8g=="},
}

func RuntimePlatform(platform, arch string) string {
	if platform == "windows" || platform == "win32" {
		platform = "win"
	}
	if arch == "amd64" {
		arch = "x64"
	}
	key := platform + "-" + arch
	if _, ok := runtimes[key]; ok && key != "sherpa-onnx-node" {
		return key
	}
	return ""
}
func origins(official, mirror, source string) []string {
	switch source {
	case "official":
		return []string{official}
	case "mirror":
		return []string{mirror}
	default:
		return []string{official, mirror}
	}
}
func RuntimeItems(platform, source string) []Item {
	items := []Item{}
	for _, pkg := range []string{"sherpa-onnx-node", "sherpa-onnx-" + platform} {
		key := strings.TrimPrefix(pkg, "sherpa-onnx-")
		if pkg == "sherpa-onnx-node" {
			key = pkg
		}
		pin := runtimes[key]
		name := fmt.Sprintf("%s-%s.tgz", pkg, SherpaVersion)
		urls := []string{}
		for _, origin := range origins("https://registry.npmjs.org", "https://registry.npmmirror.com", source) {
			urls = append(urls, origin+"/"+pkg+"/-/"+name)
		}
		marker := "sherpa-onnx.node"
		if pkg == "sherpa-onnx-node" {
			marker = "sherpa-onnx.js"
		}
		items = append(items, Item{"runtime", "package", Asset{name, pin.Bytes, DownloadHash{"sha512", "base64", pin.Integrity}, urls}, pkg, marker})
	}
	return items
}
func ModelItems(source string) []Item {
	items := []Item{}
	for _, pin := range []struct {
		Step, Name                 string
		Bytes                      int64
		Hash, Repository, Revision string
	}{
		{"model", "model.int8.onnx", 239233841, "c71f0ce00bec95b07744e116345e33d8cbbe08cef896382cf907bf4b51a2cd51", "csukuangfj/sherpa-onnx-sense-voice-zh-en-ja-ko-yue-2024-07-17", "2365baeacb507f821a0c8120fcee3d484dba7a07"},
		{"model", "tokens.txt", 315894, "f449eb28dc567533d7fa59be34e2abca8784f771850c78a47fb731a31429a1dc", "csukuangfj/sherpa-onnx-sense-voice-zh-en-ja-ko-yue-2024-07-17", "2365baeacb507f821a0c8120fcee3d484dba7a07"},
		{"vad", "silero_vad.onnx", 1807522, "a35ebf52fd3ce5f1469b2a36158dba761bc47b973ea3382b3186ca15b1f5af28", "csukuangfj/vad", "fba88cd2e921609e7675c3aaf51e0b9b295da4bc"},
	} {
		urls := []string{}
		for _, origin := range origins("https://huggingface.co", "https://hf-mirror.com", source) {
			urls = append(urls, origin+"/"+pin.Repository+"/resolve/"+pin.Revision+"/"+pin.Name)
		}
		items = append(items, Item{pin.Step, "file", Asset{pin.Name, pin.Bytes, DownloadHash{"sha256", "hex", pin.Hash}, urls}, pin.Name, ""})
	}
	return items
}
func InstallItems(platform, source string) []Item {
	items := []Item{}
	if platform != "" {
		items = append(items, RuntimeItems(platform, source)...)
	}
	return append(items, ModelItems(source)...)
}
func TotalDownloadBytes(items []Item) int64 {
	var n int64
	for _, item := range items {
		n += item.Asset.Bytes
	}
	return n
}

type Layout struct{ Base, Models, Downloads, Staging, RuntimeRoot, Manifest string }

func SenseVoiceLayout(dataRoot string) Layout {
	base := filepath.Join(dataRoot, "sensevoice")
	return Layout{base, filepath.Join(base, "models"), filepath.Join(base, "downloads"), filepath.Join(base, "staging"), filepath.Join(base, "runtime", SherpaVersion), filepath.Join(base, "verified.json")}
}
func ItemPath(layout Layout, item Item) string {
	if item.Kind == "package" {
		return filepath.Join(layout.RuntimeRoot, "node_modules", item.Target)
	}
	return filepath.Join(layout.Models, item.Target)
}
func DownloadPath(layout Layout, item Item) string {
	if item.Kind == "package" {
		return filepath.Join(layout.Downloads, item.Asset.Name)
	}
	return ItemPath(layout, item)
}
func ProofPath(layout Layout, item Item) string {
	if item.Kind == "package" {
		return filepath.Join(ItemPath(layout, item), item.Marker)
	}
	return ItemPath(layout, item)
}
func ManifestName(layout Layout, item Item) string {
	value, _ := filepath.Rel(layout.Base, ProofPath(layout, item))
	return strings.ReplaceAll(filepath.ToSlash(value), "\\", "/")
}
