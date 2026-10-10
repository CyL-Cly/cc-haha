module github.com/NanmiCoder/cc-haha/backend

go 1.25.7

require github.com/nlnwa/whatwg-url v0.6.2

require github.com/ebitengine/purego v0.9.1

require (
	github.com/petermattis/goid v0.0.0-20260113132338-7c7de50cc741 // indirect
	golang.org/x/exp v0.0.0-20260212183809-81e46e3db34a // indirect
	filippo.io/edwards25519 v1.1.0 // indirect
	github.com/beeper/argo-go v1.1.2 // indirect
	github.com/charmbracelet/colorprofile v0.4.3 // indirect
	github.com/charmbracelet/ultraviolet v0.0.0-20260703014108-f5a850f9c2b7 // indirect
	github.com/charmbracelet/x/termios v0.1.1 // indirect
	github.com/charmbracelet/x/windows v0.2.2 // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/elliotchance/orderedmap/v3 v3.1.0 // indirect
	github.com/lucasb-eyer/go-colorful v1.4.0 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mattn/go-runewidth v0.0.23 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/rs/zerolog v1.34.0 // indirect
	github.com/vektah/gqlparser/v2 v2.5.27 // indirect
	github.com/xo/terminfo v0.0.0-20220910002029-abceb7e1c41e // indirect
	go.mau.fi/util v0.9.6 // indirect
	golang.org/x/crypto v0.48.0 // indirect
	golang.org/x/sync v0.21.0 // indirect
	modernc.org/libc v1.73.4 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)

require (
	github.com/dlclark/regexp2/v2 v2.8.1
	github.com/dop251/goja v0.0.0-20261002135814-104bc28c3abd
	github.com/go-sourcemap/sourcemap v2.1.3+incompatible // indirect
	github.com/google/pprof v0.0.0-20250317173921-a4b03ec1a45e
)

require (
	github.com/bits-and-blooms/bitset v1.24.4 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/klauspost/compress v1.20.1
	github.com/spf13/cobra v1.10.2
	github.com/spf13/pflag v1.0.9
	golang.org/x/net v0.50.0
	golang.org/x/text v0.34.0
)

require github.com/skip2/go-qrcode v0.0.0-20200617195104-da1b6568686e

require (
	charm.land/bubbletea/v2 v2.0.9
	github.com/andybalholm/brotli v1.2.0
	github.com/charmbracelet/x/ansi v0.11.7
	github.com/charmbracelet/x/term v0.2.2
	github.com/coder/websocket v1.8.14
	github.com/fsnotify/fsnotify v1.10.1
	github.com/google/uuid v1.6.0
	github.com/rivo/uniseg v0.4.7
	github.com/yuin/goldmark v1.7.13
	go.mau.fi/libsignal v0.2.1
	go.mau.fi/whatsmeow v0.0.0-20260218135554-9cbe80fb25a4
	go.yaml.in/yaml/v3 v3.0.4
	golang.org/x/sys v0.46.0
	google.golang.org/protobuf v1.36.11
	modernc.org/sqlite v1.53.0
)


replace go.mau.fi/libsignal => ./third_party/libsignal-node

replace go.mau.fi/whatsmeow => ./third_party/whatsmeow-node
