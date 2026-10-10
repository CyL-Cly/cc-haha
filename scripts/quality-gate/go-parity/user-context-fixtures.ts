export type ContextStep = { kind: 'read' | 'clock' | 'override' | 'writeMode' | 'resetSettings' | 'clearUser' | 'clearSessionDate' | 'injection' | 'schema' | 'modeSources' | 'flagSettings' | 'mutateUser', value?: unknown, source?: string, delete?: boolean, raw?: string, sources?: unknown[] }
export type UserContextProfile = { name: string, timezone: string, steps: ContextStep[], memory?: 'plain-delayed', customSystemPrompt?: string }
const read: ContextStep = { kind: 'read' }, clock = (value: string): ContextStep => ({ kind: 'clock', value }), override = (value: unknown): ContextStep => ({ kind: 'override', value }), mode = (value: string, source = 'user'): ContextStep => ({ kind: 'writeMode', value, source }), reset: ContextStep = { kind: 'resetSettings' }
export const userContextProfiles: UserContextProfile[] = [
  { name: 'midnight-utc-same-mode', timezone: 'UTC', steps: [read, clock('2026-10-02T00:00:01.000Z'), read, read] },
  { name: 'midnight-shanghai-same-mode', timezone: 'Asia/Shanghai', steps: [clock('2026-10-01T15:59:59.000Z'), read, clock('2026-10-01T16:00:01.000Z'), read] },
  { name: 'midnight-los-angeles-same-mode', timezone: 'America/Los_Angeles', steps: [clock('2026-10-02T06:59:59.000Z'), read, clock('2026-10-02T07:00:01.000Z'), read] },
  { name: 'override-changes-cached-prefix', timezone: 'UTC', steps: [override('2026-01-02'), read, override('2030-03-04'), read] },
  { name: 'override-truthy-noniso-external', timezone: 'UTC', steps: [override('not-an-ISO-date'), read, override('another literal'), read] },
  { name: 'override-empty-then-string', timezone: 'UTC', steps: [override(''), read, override('2040-05-06'), read] },
  { name: 'override-deleted-after-cache', timezone: 'UTC', steps: [override('2025-01-01'), read, { kind: 'override', delete: true }, read] },
  { name: 'mode-changes-across-midnight', timezone: 'UTC', steps: [read, clock('2026-10-02T00:00:01.000Z'), mode('claude-md'), reset, read] },
  { name: 'mode-cycle-refreshes-each-return', timezone: 'UTC', steps: [mode('claude-md'), reset, read, clock('2026-10-02T00:00:01.000Z'), mode('managed-only'), reset, read, clock('2026-10-03T00:00:01.000Z'), mode('claude-md'), reset, read] },
  { name: 'same-normalized-mode-keeps-cache', timezone: 'UTC', steps: [read, clock('2026-10-02T00:00:01.000Z'), mode('unknown-mode'), reset, read] },
  { name: 'project-mode-does-not-switch', timezone: 'UTC', steps: [read, clock('2026-10-02T00:00:01.000Z'), mode('managed-only', 'project'), reset, read] },
  { name: 'user-mode-write-without-settings-reset', timezone: 'UTC', steps: [read, clock('2026-10-02T00:00:01.000Z'), mode('managed-only'), read, reset, read] },
  { name: 'explicit-user-cache-clear', timezone: 'UTC', steps: [read, clock('2026-10-02T00:00:01.000Z'), { kind: 'clearUser' }, read] },
  { name: 'system-injection-clears-user-context', timezone: 'UTC', steps: [read, clock('2026-10-02T00:00:01.000Z'), { kind: 'injection', value: 'original-debug-injection' }, read] },
  { name: 'session-date-clear-is-independent', timezone: 'UTC', steps: [read, clock('2026-10-02T00:00:01.000Z'), { kind: 'clearSessionDate' }, read] },
  { name: 'override-raw-number-bun-view', timezone: 'UTC', steps: [override(123), read, override(0), read] },
  { name: 'custom-system-helper-retains-date', timezone: 'UTC', customSystemPrompt: 'fixture-system', steps: [read, clock('2026-10-02T00:00:01.000Z'), read] },
  { name: 'real-user-memory-completes-after-midnight', timezone: 'UTC', memory: 'plain-delayed', steps: [read, read] },
  { name: 'override-lone-utf16-prefix', timezone: 'UTC', steps: [override('\ud800'), read, { kind: 'clearUser' }, override('\udfff'), read] },
  { name: 'override-raw-false-null', timezone: 'UTC', steps: [override(false), read, { kind: 'clearUser' }, override(null), read] },
]
const schema = (name: string, raw: string) => userContextProfiles.push({ name: 'plugin-schema-' + name, timezone: 'UTC', steps: [{ kind: 'schema', raw }] })
schema('ordered-strip-options-mcp', '{"future":{"x":1},"pluginConfigs":{"agents-md@builtin":{"future":"strip","options":{"instructionFiles":"claude-md","x":-0,"a":["a","\\ud800"]},"mcpServers":{"one":{"env":"x","port":1}}},"other":{"options":{"on":true}}},"model":"fixture-model"}')
schema('unknown-options-raw-keys', '{"pluginConfigs":{"\\ud800":{"options":{"\\udfff":"\\ud800","2":true,"1":["x"]},"future":1},"�":{"mcpServers":{}}}}')
for (const [name, raw] of [['null', '{"pluginConfigs":null}'], ['array', '{"pluginConfigs":[]}'], ['plugin-null', '{"pluginConfigs":{"agents-md@builtin":null}}'], ['options-null', '{"pluginConfigs":{"agents-md@builtin":{"options":null}}}'], ['server-number', '{"pluginConfigs":{"x":{"mcpServers":{"s":123}}}}'], ['union-null', '{"pluginConfigs":{"x":{"options":{"a":null,"b":{},"c":[1]}}}}'], ['overflow', '{"pluginConfigs":{"x":{"options":{"n":1e400}}}}']]) schema(name!, raw!)
const config = (options: unknown) => ({ pluginConfigs: { 'agents-md@builtin': { options } } })
for (const [name, sources] of [['precedence', [config({ instructionFiles: 'managed-only' }), config({ instructionFiles: 'claude-md' }), config({ instructionFiles: 'claude-md-and-agents-md' })]], ['legacy-none', [null, config({ projectInstructions: 'none' })]], ['legacy-both', [config({ projectInstructions: 'both' })]], ['legacy-default', [config({ instructionFiles: 'garbage', projectInstructions: 'legacy' })]], ['fallback', [config({ projectInstructions: 'agents-fallback' })]]]) userContextProfiles.push({ name: 'mode-sources-' + name, timezone: 'UTC', steps: [{ kind: 'modeSources', sources: sources as unknown[] }] })
schema('empty-hooks-order', '{"allowManagedHooksOnly":true,"disableAllHooks":false,"hooks":{"StopFailure":[],"Stop":[]},"effortLevel":"high","pluginConfigs":{"agents-md@builtin":{"options":{"instructionFiles":"claude-md"}}}}')
schema('empty-all-hook-events', '{"hooks":{"PreToolUse":[],"PostToolUse":[],"PostToolUseFailure":[],"PermissionRequest":[],"Notification":[],"UserPromptSubmit":[],"SessionStart":[],"SessionEnd":[],"Stop":[],"StopFailure":[],"SubagentStart":[],"SubagentStop":[],"PreCompact":[],"PostCompact":[],"Setup":[],"TeammateIdle":[],"TaskCompleted":[],"Elicitation":[],"ElicitationResult":[],"ConfigChange":[],"WorktreeCreate":[],"WorktreeRemove":[],"InstructionsLoaded":[]}}')
schema('hook-booleans-errors', '{"disableAllHooks":"false","allowManagedHooksOnly":null}')
schema('cleanup-zero-before-env-model', '{"future":"unknown","model":"fixture-model","env":{"A":1},"cleanupPeriodDays":0}')

userContextProfiles.push(
  { name: 'override-json-number-extremes', timezone: 'UTC', steps: [{kind:'override',raw:'1e400'},read,{kind:'clearUser'},{kind:'override',raw:'-0'},read] },
  { name: 'local-year-unpadded', timezone: 'UTC', steps: [clock('0001-01-02T00:00:00.000Z'), read] },
  { name: 'flag-settings-real-cache-and-mode', timezone: 'UTC', steps: [read,{kind:'flagSettings',value:config({instructionFiles:'managed-only'})},read,reset,read,{kind:'flagSettings',value:null},reset,read] },
  { name: 'cached-user-object-original-mutation', timezone: 'UTC', steps: [read,{kind:'mutateUser',value:'changed original memo object'},read] },
)
