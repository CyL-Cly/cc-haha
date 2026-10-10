# Native SDK image/document input — 2026-10-08

Scope: urgent signed-desktop P213 failure `headless session capability is not migrated: SDK native media content block image`. All fixtures use disposable HOME/config/work and isolated HTTP/TLS; no real user image, provider config, Keychain, or external telemetry is used. Official DeepSeek GUI evidence is separately owned by the parent and is not claimed below.

## Cause and implementation

`sessioncontroller/admission.go` accepted only text blocks despite the actual Source CLI forwarding SDK arrays into `processUserInput`. `sdkhost.slashParts` also assumed the last array block had `.text`, breaking image-only/trailing-image prompts. The real public stream-input path did not establish the same input processing lifetime already used by `RunOne` and `RunDequeued`.

The native `PrintOptions.NativeInputMedia` callback is bound to the existing MainPrint Owner. Only immutable input bytes/library location reach `nativeSkillWork`, using the existing native copy ledger; actual codec work returns immutable normalized blocks and metadata texts. `InputProcessor.NativeMedia` is set only when NativeProcess and this callback are present. Merely setting NativeTools does not allow an unbound image producer.

`imageprocessing.InputBlocks` ports the original top-level image pass: Node-compatible base64 decode; real bundled libvips normalization; rebuilding base64 image blocks with the actual MIME/data and Source field dropping; URL images and documents remain unchanged. Size metadata is a real `isMeta` user message, after turn attachments and before model preparation. Input arrays ending in a media block remain ordinary prompts. The existing mixed slash/preceding-block capability guard remains explicit.

History and requestretry admit supported SDK document sources (base64 PDF, text, URL, content string/content blocks), preserving title/context/citations/cache-control and raw future fields. URL images are passed to the model API; the input processor does not download them. No image path is authorized by supplying raw base64. Existing scoped image/PDF error recovery remains the original normalizer.

## Actual Source proof

- `native-sdk-input-media.fixture.ts` extracts the actual `processUserInputBase` and `addImageMetadataMessage` declarations using Acorn, calls the actual `maybeResizeAndDownsampleImageBlock` and `processTextPrompt`, and uses the disposable Source capability sandbox. It tests 22 input rows and 11 actual Node base64 cases, including image-only, leading/trailing image, width/height over 8000, incorrect MIME, camel mediaType, unknown image fields, URL image and every supported document source. First nil/empty metadata observation mismatch is retained in the initial log; second run passes.
- The existing actual `normalizeMessagesForAPI` fixture now covers 36 scenarios including 16 new URL/document/pass-through/recovery/nested-document cases. Twelve actual Source SDK HTTP and actual Go loopback HTTP requests compare the full request observations, with immutable original history checks. No Source production fallback exists.

## Actual native consumers

- MainPrint / same Owner / TLS model endpoint / SDK image-only or trailing-image / normalized canonical JSONL / complete shutdown / new MainPrint with the same session ID / next real POST retaining image and assistant response.
- MainPrint real 9001x3 PNG -> 8000x3 native image / exact metadata canonical message / five document variants and URL images / raw source fields in the actual POST. Initial test expectation failed on the real currentDate context and Source message-merge newline; the expectation was corrected without changing production to hide that context.
- Real server WebSocket desktop image attachment -> staged PNG bytes -> actual default catalog (at least 19 tools) -> model POST -> message complete/idle -> close -> cold same-ID desktop factory resume -> second POST retaining actual image/reply.
- Actual ordinary CLI `RunCommand` stream-json stdin invokes the public MainPrint input iterator and returns successful structured result, using the full default tool catalog. Its initial failure exposed the input lifetime gap; RunStream now binds the existing context solely around original BeginDequeued.
- Foreign Owner and pre-cancelled normalization are rejected, and normal shutdown joins the same native ownership ledger.

## Commands and observations

All Go commands use the already hydrated offline environment from `/tmp/go-openaiauth-final-environment.json`, Go 1.25.7, `-mod=readonly`, no dependency changes.

- FAILED first, actual original controller guard: `/tmp/go-sdk-input-media-red-first.log`.
- PASSED actual MainPrint first image: `/tmp/go-sdk-input-media-native-second.log` (2.317s).
- FAILED first Source metadata empty-vs-nil observation: `/tmp/go-sdk-input-media-source-third.log`.
- PASSED actual Source input/normalization/physical requests: `/tmp/go-sdk-input-media-source-and-physical-fourth.log` (imageprocessing 2.147s, requestcontext 25.873s).
- FAILED draft test helper symbol: `/tmp/go-sdk-input-media-canonical-resume-fifth.log`; fixed to existing `cooperative.New`.
- FAILED full actual MainPrint document requestretry guard: `/tmp/go-sdk-input-media-canonical-resume-sixth.log`.
- PASSED stable compilation: `/tmp/go-sdk-input-media-stable-seventh.log`.
- FAILED first actual size/meta expectation, original context retained: `/tmp/go-sdk-input-media-full-native-eighth.log`.
- PASSED full actual MainPrint image/doc/cold resume plus original image retry admission: `/tmp/go-sdk-input-media-full-native-ninth.log` (CLI 4.177s, retry 1.614s).
- FAILED draft test RunCommand arity: `/tmp/go-sdk-input-media-real-desktop-cli-tenth.log`.
- FAILED actual public CLI input-stage lifetime, desktop test passed: `/tmp/go-sdk-input-media-real-desktop-cli-eleventh.log`.
- PASSED actual server WS/default catalog/cold resume and ordinary CLI stdin: `/tmp/go-sdk-input-media-real-desktop-cli-twelfth.log` (3.697s).
- PASSED full seven-package actual Source/race: `go test -mod=readonly -race -p 2 ./internal/imageprocessing ./internal/requestcontext ./internal/requestretry ./internal/sessioncontroller ./internal/sdkhost ./internal/clibootstrap ./internal/application -count=1 -timeout 12m`, log `/tmp/go-sdk-input-media-seven-full-source-race-thirteenth.log` (exit 0; imageprocessing 60.948s / requestcontext 29.588s / requestretry 3.650s / controller 38.154s / SDK 16.836s / CLI 477.099s / application 219.729s).
- PASSED seven-package `go vet -mod=readonly`, `/tmp/go-sdk-input-media-seven-vet-fourteenth.log` (exit 0); `git diff --check` passed.

Pending: parent fresh signed app / isolated PNG / official DeepSeek GUI validation; no real vision provider response is substituted by fixture output. Unsupported file image source and mixed array slash dispatch remain explicit guards outside this urgent regular-chat profile.
