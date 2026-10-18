import { identityDeprecatedCatalog } from './query-loop-identity-fixtures'
// Original registry definition, shared before either language renders a schema.
// This is z.object(...), whose unknown fields are stripped by execution parsing.
export const queryLoopToolDefinition = { kind: 'registered', name: 'FixtureWrite', aliases: [] as string[], inputSchema: { kind: 'object', unknownKeys: 'strip', fields: [{ name: 'file_path', schema: { kind: 'string' } }, { name: 'content', schema: { kind: 'string' } }] }, description: 'Write one private fixture file', prompt: 'Write one private fixture file', concurrencySafe: false, readOnly: false }
export type QueryLoopScenario = { name: string; nonInteractive: boolean; backfill: 'add' | 'overwrite' | 'none'; permission: 'allow' | 'replace-path' | 'replace-content'; resultContentSource: string; wires: string[]; historySources: string[]; systemSources: string[]; definition: typeof queryLoopToolDefinition; maxOutputTokens: number; identityTrace?: boolean; refreshTools?: 'replace-after-tools'; deprecatedCatalog?: typeof identityDeprecatedCatalog }
export type QueryLoopInput = QueryLoopScenario & { home: string; origin: string; runtime: { platform: string; arch: string; version: string }; deviceID: string; sessionID: string }
export type QueryLoopObservation = {
  profile: { nonInteractive: boolean; querySource: string; streamingToolExecution: boolean }
  modelHistory: string[][]
  history: string[]
  requests: Array<{ path: string; method: string; headers: Array<[string, string]>; body: string }>
  yielded: string[]
  yieldedAfter: string[]
  effects: Array<{ kind: string; value: string }>
  allocations: Array<{ kind: 'uuid' | 'time'; value: string }>
  terminal: unknown
  error: null | { name: string; message: string }
  executions: number
  file: string | null
}
const frame = (type: string, rest: Record<string, unknown>) => `event: ${type}\ndata: ${JSON.stringify({ type, ...rest })}\n\n`
const start = (id: string) => frame('message_start', { message: { id, type: 'message', role: 'assistant', model: 'fixture-model', content: [], stop_reason: null, stop_sequence: null, usage: { input_tokens: 10, output_tokens: 0 } } })
const first = (partial = '{"file_path":"effect.txt","content":"written exactly once"}') => start('fixture-message-1') + [0, 1].map(index => frame('content_block_start', { index, content_block: { type: 'tool_use', id: 'fixture-call', name: 'FixtureWrite', input: {} } }) + frame('content_block_delta', { index, delta: { type: 'input_json_delta', partial_json: partial } }) + frame('content_block_stop', { index }) + frame('content_block_stop', { index })).join('') + frame('message_delta', { delta: { stop_reason: 'end_turn', stop_sequence: null }, usage: { output_tokens: 5 } }) + frame('message_stop', {})
const second = start('fixture-message-2') + frame('content_block_start', { index: 0, content_block: { type: 'text', text: '' } }) + frame('content_block_delta', { index: 0, delta: { type: 'text_delta', text: 'complete' } }) + frame('content_block_stop', { index: 0 }) + frame('message_delta', { delta: { stop_reason: 'end_turn', stop_sequence: null }, usage: { output_tokens: 3 } }) + frame('message_stop', {})
const original = { historySources: ['{"type":"user","message":{"role":"user","content":"Run the fixture write once"},"uuid":"fixture-user","timestamp":"2023-11-14T22:13:20.000Z"}'], systemSources: [] as string[], definition: queryLoopToolDefinition, maxOutputTokens: 32000, deprecatedCatalog: identityDeprecatedCatalog.filter(tool => tool.name !== 'ToolSearch'), resultContentSource: '"written"' }
export const queryLoopScenarios: QueryLoopScenario[] = [
  ...[true, false].flatMap(nonInteractive => (['add', 'overwrite', 'none'] as const).map(backfill => ({ ...original, name: `${nonInteractive ? 'headless' : 'interactive'}-duplicate-end-turn-${backfill}`, nonInteractive, backfill, permission: 'allow' as const, wires: [first(), second] }))),
  ...[true, false].map(nonInteractive => ({ ...original, name: `${nonInteractive ? 'headless' : 'interactive'}-strip-only-execution`, nonInteractive, backfill: 'none' as const, permission: 'allow' as const, wires: [first('{"content":"written exactly once","unknown":"kept in model history","\\ud800":"future field","file_path":"effect.txt"}'), second] })),
  { ...original, name: 'headless-permission-replaces-path', nonInteractive: true, backfill: 'overwrite', permission: 'replace-path', wires: [first(), second] },
  { ...original, name: 'interactive-permission-replaces-content', nonInteractive: false, backfill: 'add', permission: 'replace-content', wires: [first(), second] },
  { ...original, name: 'headless-empty-result-marker', nonInteractive: true, backfill: 'none', permission: 'allow', resultContentSource: '"\\ufeff\\u2003\\t"', wires: [first(), second] },
  { ...original, name: 'interactive-strip-result-references', nonInteractive: false, backfill: 'none', permission: 'allow', resultContentSource: '[{"type":"tool_reference","tool_name":"FixtureWrite"},{"type":"text","text":"visible\\ud800"}]', wires: [first(), second] },
]

// Original fault wire and scalar registry definitions precede both languages.
// Repeat tool IDs are suppressed by the real Controller, while distinct raw
// surrogate IDs retain separate progress, handler, mapper and request identity.
const identityFirst = (uses: Array<{ name: string; id: string; partial?: string }>) => start('fixture-message-1') + uses.map((use, index) =>
  frame('content_block_start', { index, content_block: { type: 'tool_use', name: use.name, id: use.id, input: {} } }) +
  frame('content_block_delta', { index, delta: { type: 'input_json_delta', partial_json: use.partial ?? '{"file_path":"effect.txt","content":"written exactly once"}' } }) +
  frame('content_block_stop', { index }) + frame('content_block_stop', { index }),
).join('') + frame('message_delta', { delta: { stop_reason: 'end_turn', stop_sequence: null }, usage: { output_tokens: 5 } }) + frame('message_stop', {})
for (const nonInteractive of [true, false]) {
  const cases = [
    { name: 'distinct-lone-ID-repeat', definition: queryLoopToolDefinition, uses: [{ name: 'FixtureWrite', id: '\ud800' }, { name: 'FixtureWrite', id: '\ud800' }, { name: 'FixtureWrite', id: '�' }, { name: 'FixtureWrite', id: '\udfff' }] },
    { name: 'unknown-high-name-then-replacement', definition: { ...queryLoopToolDefinition, name: '�' }, uses: [{ name: '\ud800', id: 'unknown' }, { name: '�', id: '\ud800' }] },
    { name: 'unknown-low-name-then-alias', definition: { ...queryLoopToolDefinition, aliases: ['�'] }, uses: [{ name: '\udfff', id: 'unknown' }, { name: '�', id: '\udfff' }] },
    { name: 'unknown-primitive-input', definition: queryLoopToolDefinition, uses: [{ name: '\ud800', id: '\udfff', partial: 'null' }] },
    { name: 'marker-before-unknown-lookup', definition: queryLoopToolDefinition, uses: [{ name: '\ud800', id: '\ud800', partial: '{"' + '😀'.repeat(110) + '\udfff' }] },
  ]
  for (const item of cases) queryLoopScenarios.push({ ...original, name: `${nonInteractive ? 'headless' : 'interactive'}-identity-${item.name}`, nonInteractive, backfill: 'add', permission: 'allow', identityTrace: true, definition: item.definition, wires: [identityFirst(item.uses), second] })
}

// The real interactive release snapshot enables Task V2; SDK profile does not.
for (const scenario of queryLoopScenarios) {
  const catalog = identityDeprecatedCatalog.filter(tool => tool.name !== 'ToolSearch')
  if (!scenario.nonInteractive) catalog.splice(catalog.findIndex(tool => tool.name === 'EnterPlanMode') + 1, 0, ...['TaskCreate', 'TaskGet', 'TaskUpdate', 'TaskList'].map(name => ({ name, aliases: [] })))
  scenario.deprecatedCatalog = catalog
}
