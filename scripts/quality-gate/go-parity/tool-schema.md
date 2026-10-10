# Native tool schema construction checkpoint

This slice migrates model-visible schema/prompt construction for the production
Read, Edit, Write, Glob, and Grep definitions. It also renders explicitly
registered original definitions from an ordered schema declaration and a literal
original prompt. It does not migrate the five builtin tools' execution handlers.
The production CLI guard remains in place; this is not full Harness or Benchmark
acceptance.

## Source and independent construction

The actual Source callers are `src/services/api/claude.ts` (`queryModelWithStreaming`
then `toolToAPISchema`) and `src/utils/api.ts` (`toolToAPISchema`,
`normalizeToolInputForAPI`). Original builtin declarations and prompt recipes
come from `src/tools/{FileReadTool,FileEditTool,FileWriteTool,GlobTool,GrepTool}`.
Read's model resolution uses `src/utils/model/model.ts:getMainLoopModel`, and its
first-use feature snapshot is `FileReadTool/limits.ts`. The session base-schema
cache is `src/utils/toolSchemaCache.ts`.

The Source child imports the actual five definitions and actual Zod conversion.
The prompt spy delegates the complete production prompt method and only counts
calls. The three feature-store controls (`tengu_amber_wren`,
`tengu_compact_line_prefix_killswitch`, `tengu_fgts`) and strict gate are declared
primitive host inputs. Go copies the original builtin declarations and prompt
recipes into executable code, renders them independently, then `requestcontext`
builds the complete request from original history/config/tool descriptors. Go
never receives a captured Source schema, description, prepared body, or path.
A registered definition's literal prompt and original schema declaration are
inputs to both implementations before either one renders a schema.

The schema DSL covers object/string/boolean/number/enum declarations, optional
fields, default values, descriptions, integer/range bounds, and object
strip/strict/passthrough JSON-schema output. It preserves the actual Zod 2020-12
property/required order, including JavaScript numeric property enumeration.
Opaque original `inputJSONSchema` objects retain their original cache-key and
rendering semantics. Unsupported declaration fields/transforms, unpaired UTF-16
definition names, swarm/plan/tool-search/MCP definitions, and unknown builtins
fail closed. The execution `ParseInput` profile is narrower: registered strip
objects with required/optional string fields, ordered issues, and unknown-key
stripping. Other execution schemas return a capability error; Zod stack/pretty
error formatting is not claimed.

## Timing and request integration

`toolschema.Session` stores session-stable base fields. A batch starts with a
cache snapshot, matching the Source Promise.all behavior where duplicate missing
names each invoke an immediately resolving prompt. Defer/cache overlays apply
per call; kill switches remove only the Source-selected fields. Explicit cache
clear retains the independently memoized Read limits. Literal registered
callbacks and the five immediate builtin prompt recipes are supported; arbitrary
asynchronous/permission/agent-dependent prompts are not implemented.

`requestcontext.Host.ToolSchemas` retains a session across rounds, and
`ToolSchemaHost` supplies original prompt/feature inputs. The actual relay query
caller passes no `cacheControl` to toolToAPISchema; Go does the same and appends
extra schemas after native schemas. Historical tool names use UTF-16 identity
for aliases, while historical tool input is preserved. Only the actual Edit
normalizer strips its old synthetic fields when `edits` exists. Execution parse
results must never replace that historical input. Native query-loop integration
uses these APIs and the same raw registered descriptor across two rounds.

## Deterministic evidence

Pinned Bun 1.3.14, SDK 0.80.0, TRANSCRIPT_CLASSIFIER release feature, and the Source
empty BUILD_TIME macro are used. All Source imports occur after disposable
HOME/config validation and capability installation. The declared loopback origin
is the only permitted network origin. Secure storage is an observed null external
adapter; the two platform-managed policy paths are explicit ENOENT adapters.
These capability observations are Source-only evidence. Six negative guards
cover home/root/symlink/env/network/native access; five comparison mutants reject
changed description/schema/cache/order/request bytes.

There are 46 actual Source profiles: both Source self replay and independent Go
parity are run for each. Eleven comparator/isolation checks bring the focused
suite to 103 tests. `backend/internal/toolschema/testdata/tool_schema_contract.json`
contains the actual Source captures, used only as native expected results.
The fixture records all resolved pre-fetch application headers and exact final
body/path; Go also checks those fields against its actual loopback request.
Automatic Bun/Go transport headers are outside this slice's comparison, so these
results do not establish complete HTTP wire equivalence.

Actual red-to-green counterexamples included JS toFixed midpoint/exponent output,
Zod default-description property order, numeric-key required/issues order, and a
lone high surrogate wrongly matching a literal U+FFFD alias. Actual external
host-policy replay also rejects strict/eager fields for internal-only staging
and non-default api.anthropic.com ports. A prior Read Haiku
fixture using initial model instead of the actual current-model override was
corrected; its early evidence is superseded by the final corpus.

Focused logs are `/tmp/cc-haha-tool-schema-final46.log` and
`/tmp/cc-haha-request-context-tool-schema-backcompat.log`. Current-native Go
commands/report are in ignored `artifacts/go-parity/tool-schema-native/`, with
`/tmp/cc-haha-tool-schema-native-final46.log` as the command log. The existing 90%
changed-executable-line gate is evaluated from HEAD against both owned Go areas;
aggregate pass is not a claim that each file independently reaches 90%.
Only current Darwin ARM64 native execution is collected here. The full unified
corpus, all-package gate, five other native targets, production startup wiring,
remaining builtin definitions/handlers, MCP/tool search, first-party request
construction, real model quota, and Benchmark non-inferiority remain separate
migration work.
