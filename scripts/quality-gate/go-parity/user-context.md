# Original user/date context compatibility

This component ports the release user/date context memo before request preparation. It does not run normal CLI startup or a complete SDK query engine.

## Source boundary

The oracle invokes actual production exports after private HOME/config and capability guards: `src/constants/common.ts:getLocalISODate/getSessionStartDate`, `src/context.ts:getUserContext/setSystemPromptInjection`, `src/utils/instructionFiles.ts`, the settings file parser/cache/bootstrap setters, `fetchSystemPromptParts` with an original custom system prompt, and `prependUserContext`. The date resolver, memory producer, parser and business caches are not replaced. The external Date/UUID allocators are fixed; import allocations remain Source-only evidence, and the compared allocation scope starts after imports.

`getLocalISODate` returns the truthy original environment override without ISO validation, otherwise the current local calendar date with an unpadded year. `getSessionStartDate` has an independent memo. The user context holds one current mode promise and result reference; mode changes, explicit user-cache clear and the injection setter refresh that memo. Midnight, environment changes, same normalized mode and settings reset alone do not refresh an existing same-mode result. Date sampling occurs after the native memory read completes, and clearing the lookup or canceling a waiter does not cancel that running producer.

## Production APIs

`usercontext.New(Options)` requires the original scalar environment, real CLI settings service/state, a clock and the explicit external no-active-plugin profile. `GetUserContext()` returns a shared `*Future`; `Await(ctx)` returns the same mutable `*protocol.JSONObject`. The host serializes environment and result mutations as Source's JS host does. `GetSessionStartDate`/`ClearSessionStartDate` are independent of `ClearUserContext`. `SetFlagSettings` retains the actual bootstrap behavior: it does not reset the settings cache. `SetSystemPromptInjection` clears the user memo on every call; this component does not implement the separate system/Git context memo.

`Controller.ResolveContext(ctx, originalContext)` forbids alternate history and returns an unprepared `requestcontext.Context`. It resolves original settings/environment through the retained service, uses the controller's actual original history, and creates one request-local meta-user envelope via the real factory and `messages.OrderedUser`. The returned prefix does not enter mutable transcript history. The existing `Controller.Prepare` wrapper calls this resolver and then prepares once. A shared model phase must call the resolver once per ASK and retain preparation across physical attempts; that outer integration is not proved by this component.

The legacy finite headless adapter admits original absent policy files and actual empty parsed `{}` policy files, preserving the resume service's empty object cache. Nonempty, invalid or inaccessible policy inputs require the original policy producer and are rejected. An explicitly supplied `Options.UserContext` uses the raw original environment service instead of reconstructing it from the legacy string map.

## Settings and memory scope

The parser independently implements the literal `pluginConfigs` schema: ordered plugin records, stripped unknown plugin-object fields, `mcpServers` then `options`, and primitive option values (`string`, finite JS number, boolean, string arrays). It retains raw UTF-16 keys/strings and actual validation diagnostics. This is configuration parsing, not active plugin discovery or execution.

The schema also supports genuine empty hooks objects, known release hook-event empty arrays, `disableAllHooks`/`allowManagedHooksOnly` boolean validation, and only JS numeric-zero `cleanupPeriodDays` required by an existing headless no-persistence profile. Active matcher/definitions, unknown hook events and other cleanup values remain explicit capabilities. Previously ported settings/cache/write semantics remain in place; unsupported active known fields are not treated as unknown passthrough.

Bare/disable-MD paths resolve without memory. One additional declared profile reads a real private user `CLAUDE.md`, waits for actual native I/O, formats the literal Source user-file recipe and updates classifier memory before sampling date. Its allowed input is plain valid UTF-8 text in `claude-md` mode with explicit additional directories, no policy/rules/includes/auto-memory. Other modes, rules, markdown imports/comments/frontmatter, invalid UTF-8 and native read-error projection are capability-rejected. Full project discovery, injected-memory filtering, glob/rules/PDF/attachments, active plugin producers, system/Git context and automatic date-change attachments remain pending. The custom-prompt oracle is the actual helper chain, not a whole `runHeadless` execution; the memory proof is not a general memory-loader migration.

## Evidence and reproduction

Pinned Bun 1.3.14 and Go 1.25.7 are required. Each Source child uses `--no-env-file`, production/external profile, disposable HOME/config and pre-import observable adapters: no external FS/native/network/auth, empty secure store and exact absent policy paths. Go builds use `createGoBuildEnvironment`, a private HOME/cache and the verified offline module seed with GOPROXY/GOSUMDB disabled. Do not run production exports with a real HOME.

From a disposable parent environment produced by `createSandboxedTestEnvironment`, run:

```text
bun --no-env-file test --timeout 180000 scripts/quality-gate/go-parity/user-context.test.ts
```

Ordinary runs never write goldens. `CC_HAHA_CAPTURE_USER_CONTEXT=1` is an explicit Source capture operation; `CC_HAHA_USER_CONTEXT_SOURCE_ONLY=1` runs Source-only evidence. The committed Go testdata contains actual Source observations from original operations and files, not Go-generated expectations. Per-child raw input, command, environment, stdout/stderr base64 and capability traces are retained under ignored `artifacts/go-parity/user-context-raw/`; randomized HOME replacement occurs only in comparison.

The current corpus has 42 Source self replays, 42 independent Go comparisons, 7 comparator mutants and 3 before-import guards. It covers three timezone midnight transitions, raw override values including lone UTF-16/Infinity/-0, independent date memo, mode/cache/injection lifecycle, original result mutation/shared references, real delayed private memory and actual settings parser order/errors. The old headless Controller corpus is a separate unchanged first-HTTP/persistence proof and must not be extrapolated to later tool rounds or physical retries.

The actual old `Controller.Prepare` sampled a new date on the second preparation across midnight; the new native regression first failed that implementation and now retains Source's memoized date. Source-first parser coverage also exposed the missing numeric-zero cleanup branch before it was implemented. Fixture canonical HOME and import-UUID scope failures are retained separately as observer fixes, not production semantic regressions.
