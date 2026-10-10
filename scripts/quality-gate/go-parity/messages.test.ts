import { afterAll, beforeAll, expect, test } from 'bun:test'
import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { assertTraceEqual, canonicalJsonSource, type TraceEvent } from './trace'

const rootDir = fileURLToPath(new URL('../../../', import.meta.url))
let buildHome: string
let executable: string
beforeAll(async () => {
  buildHome = await mkdtemp(join(tmpdir(), 'go-messages-build-'))
  executable = join(buildHome, process.platform === 'win32' ? 'messages.exe' : 'messages')
  const child = Bun.spawn(['go', 'build', '-o', executable, './internal/messages/testdata/fixture'], {
    cwd: resolve(rootDir, 'backend'), env: await createGoBuildEnvironment(rootDir, buildHome), stdin: 'ignore', stdout: 'pipe', stderr: 'pipe',
  })
  const timer = setTimeout(() => child.kill('SIGKILL'), 180_000)
  try { const [code, stderr] = await Promise.all([child.exited, new Response(child.stderr).text(), new Response(child.stdout).text()]); expect(code, stderr).toBe(0) } finally { clearTimeout(timer) }
}, 190_000)
afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })

type Step = { operation: string; options?: unknown; toolUse?: boolean; input?: string; preceding?: unknown[]; a?: unknown; b?: unknown; blocksA?: unknown[]; blocksB?: unknown[]; universal?: boolean; history?: unknown[]; strict?: boolean }
const text = (value: string, future?: unknown) => ({ type: 'text', text: value, ...(future === undefined ? {} : { future }) })
const toolResult = (content?: unknown, isError?: boolean) => ({ type: 'tool_result', tool_use_id: 'fixture-call', ...(content === undefined ? {} : { content }), ...(isError === undefined ? {} : { is_error: isError }), future: { keep: '\ud800' } })
const toolUse = (id: string) => ({ type: 'tool_use', id, name: 'fixture', input: { unknown: '\udfff' } })
const user = (content: unknown, meta = false, uuid = 'user-first') => ({ type: 'user', uuid, timestamp: '', isMeta: meta, toolUseResult: { source: 'first' }, message: { role: 'user', content, future: { keep: true } } })
const assistant = (content: unknown, uuid = 'assistant-first') => ({ type: 'assistant', uuid, requestId: 'request-first', message: { role: 'assistant', id: 'api-first', content, usage: { output_tokens: 12 }, future: { keep: true } } })
const resultFor = (id: string, content: unknown = 'output') => ({ type: 'tool_result', tool_use_id: id, content, future: { keep: '\ud800' } })
const serverUse = (id: string, type = 'server_tool_use') => ({ type, id, name: 'server-fixture', input: {} })
const pairingCases: Array<[string, unknown[], boolean?]> = [
  ['empty valid history', []],
  ['complete tool pairs remain byte-semantic identical', [user('start'), assistant([toolUse('one')]), user([resultFor('one')])]],
  ['missing final results create one synthetic meta envelope', [user('start'), assistant([toolUse('one'), toolUse('two')])]],
  ['missing results patch a following plain user without regenerating identity', [assistant([toolUse('one')]), user('followup')]],
  ['leading orphan preserves user-first placeholder', [user([resultFor('missing')]), assistant([text('answer')])]],
  ['leading orphan with text preserves its metadata', [user([resultFor('missing'), text('keep real input')])]],
  ['non-leading orphan-only user is removed', [user('start'), user([resultFor('missing')]), assistant([text('answer')])]],
  ['duplicate tools within and across assistant IDs are stripped once', [assistant([toolUse('one'), toolUse('one')]), user([resultFor('one'), resultFor('one')]), { ...assistant([toolUse('one')], 'second'), message: { ...assistant([]).message, id: 'api-second', content: [toolUse('one')] } }, user([resultFor('one')]), assistant([text('tail')])]],
  ['orphan results are stripped and missing results prepend in assistant order', [assistant([toolUse('one'), toolUse('two')]), user([resultFor('two'), resultFor('orphan'), resultFor('two'), text('real user')])]],
  ['orphan-only next user becomes no-content meta to retain alternation', [assistant([text('before')]), user([resultFor('orphan')]), assistant([text('after')])]],
  ['server uses require result in same assistant', [assistant([serverUse('server-one'), serverUse('mcp-one', 'mcp_tool_use'), text('keep')])]],
  ['server results permit corresponding uses and retain unknown blocks', [assistant([serverUse('server-one'), { type: 'web_search_tool_result', tool_use_id: 'server-one', content: [] }, serverUse('mcp-one', 'mcp_tool_use'), { type: 'mcp_tool_result', tool_use_id: 'mcp-one', content: 'server-output' }, { type: 'future_block', payload: 'future\ud800' }])]],
  ['originally empty assistant preserves actual no-op branch', [assistant([])]],
  ['missing tools before a following assistant insert a synthetic user', [assistant([toolUse('one')]), assistant([text('next')], 'second')]],
  ['UTF16 tool identities stay distinct from replacement characters', [assistant([toolUse('id\ud800'), toolUse('id\udfff'), toolUse('id\ufffd')]), user([resultFor('id\udfff')])]],
  ['repaired reminder siblings fold only into last tool result', [assistant([toolUse('one')]), user([text('<system-reminder> context\ud800 </system-reminder>'), text('real user')])], true],
  ['tool reference constraints preserve reminder siblings', [assistant([toolUse('one'), toolUse('two')]), user([resultFor('one', [{ type: 'tool_reference', tool_name: 'fixture' }]), text('<system-reminder>keep sibling</system-reminder>')])], true],
  ['legacy scalar IDs retain JS number equality and null distinction', [assistant([0, -0, null, false, true, 42, '42', 9007199254740992].map(id => ({ ...toolUse('old'), id }))), user([0, null, 42].map(tool_use_id => ({ ...resultFor('old'), tool_use_id }))) ]],
  ['legacy missing assistant IDs retain undefined versus null diagnostics', [user('start'), { ...assistant([toolUse('one')]), message: { role: 'assistant', content: [toolUse('one')] } }, { ...assistant([toolUse('two')]), message: { role: 'assistant', id: null, content: [toolUse('two')] } }]],
]
const fixtures: Array<[string, Step[]]> = [
  ['user default content truthiness and identity', [{}, { content: '' }, { content: null }, { content: false }, { content: 0 }, { content: [] }, { content: '中文 😀 \ud800', uuid: 'fixed', timestamp: '' }, { content: 'hello', uuid: '', timestamp: null }].map(options => ({ operation: 'user', options }))],
  ['all user metadata retains null false unknown children and UTF16', [{ operation: 'user', options: { content: [{ type: 'tool_result', tool_use_id: 'call', is_error: true, content: 'input error' }], isMeta: false, isVisibleInTranscriptOnly: true, isVirtual: true, isCompactSummary: true, summarizeMetadata: { messagesSummarized: 2, future: true }, toolUseResult: { future: [1, '中文', '\ud800'] }, mcpMeta: { _meta: { nested: { unknown: 'keep' } }, structuredContent: { proxy: true } }, imagePasteIds: [1, 3], sourceToolAssistantUUID: 'assistant-id', permissionMode: 'plan', origin: { type: 'fixture', future: true }, ignored: 'drop' } }]],
  ['assistant synthetic envelope content default usage and ID ordering', ['', '中文 😀 \ud800', [], [{ type: 'thinking', thinking: 'private', signature: 'sig' }, { type: 'tool_use', id: 'call', name: 'fixture', input: {} }]].map(content => ({ operation: 'assistant', options: { content } }))],
  ['assistant explicit usage isVirtual and unrelated errors', [{ operation: 'assistant', options: { content: '', usage: null, isVirtual: true, apiError: 'ignored' } }, { operation: 'assistant', options: { content: [], usage: { input_tokens: 5, output_tokens: 0, future: { high: '\ud800' } } } }]],
  ['API error metadata and default synthetic usage', [{ operation: 'api-error', options: { content: '', apiError: 'rate_limit', error: 'fixture', errorDetails: 'detail 中文 😀', businessErrorCode: 'code', sourceModel: 'fixture-model', usage: { ignored: 1 }, isVirtual: true } }, { operation: 'api-error', options: { content: 'failure \ud800', apiError: null, errorDetails: '', businessErrorCode: null } }]],
  ['interruption variants and input block ordering', [{ operation: 'interrupt', toolUse: false }, { operation: 'interrupt', toolUse: true }, { operation: 'prepare', input: '中文 😀 \ud800', preceding: [] }, { operation: 'prepare', input: '', preceding: [{ type: 'image', source: { type: 'base64', data: 'fixture' } }, { type: 'text', text: 'before' }] }]],
  ['user prompt seams preserve newline metadata and stable UUID', [
    { operation: 'merge-user', a: user('2 + 2'), b: user('3 + 3', false, 'second') },
    { operation: 'merge-user', a: user([text('before\ud800', { keep: 1 })], true), b: user([text('<system-reminder>after\udfff</system-reminder>')], false, 'non-meta') },
    { operation: 'merge-user', a: user([]), b: user([text('after'), toolResult('tool output')]) },
    { operation: 'merge-user', a: user([toolResult('first'), text('tail')]), b: user([toolResult('second'), text('head')]) },
  ]],
  ['assistant tool clusters retain original order and all displaced blocks', [
    [], [text('plain')], [toolUse('one'), text('tail')], [toolUse('one'), toolUse('two')],
    [{ type: 'thinking', thinking: 'head', signature: 'sig' }, toolUse('one'), text('between'), { type: 'redacted_thinking', data: 'signed' }, toolUse('two'), text('tail')],
  ].map(blocksA => ({ operation: 'reorder-tools', blocksA }))],
  ['assistant merging retains first envelope and groups tools without mutation', [{ operation: 'merge-assistant', a: assistant([toolUse('one'), text('between')]), b: assistant([{ type: 'thinking', thinking: 'middle', signature: 'sig' }, toolUse('two')], 'second') }]],
  ['legacy tool result smoosh trims JS whitespace and preserves fallback siblings', [
    { blocksA: [], blocksB: [text('after')] }, { blocksA: [text('plain')], blocksB: [text('after')] },
    { blocksA: [toolResult(' \ufeffbefore\ud800\u00a0 ')], blocksB: [text(' \u0085after\udfff\n '), text('\n\t')] },
    { blocksA: [toolResult('')], blocksB: [] }, { blocksA: [toolResult('before')], blocksB: [{ type: 'image', source: { type: 'url', url: 'fixture' } }] },
    { blocksA: [toolResult([text('array')])], blocksB: [text('after')] },
  ].map(options => ({ operation: 'merge-content', ...options, universal: false }))],
  ['universal tool result smoosh handles missing content and media but honors tool references', [
    { blocksA: [toolResult()], blocksB: [text(' \ufeffafter\ud800\n '), text(' next\udfff ')] },
    { blocksA: [toolResult(' before ')], blocksB: [{ type: 'image', source: { type: 'url', url: 'fixture' } }, text(' after '), toolResult('second')] },
    { blocksA: [toolResult([text(' first ', 'preserve-first-text-metadata'), text(' second '), { type: 'document', source: { type: 'url', url: 'fixture' } }])], blocksB: [text(' third '), text('\n'), { type: 'search_result', source: 'fixture', title: 'fixture', content: [] }] },
    { blocksA: [toolResult([{ type: 'tool_reference', tool_name: 'fixture' }])], blocksB: [text('keep sibling')] },
    { blocksA: [toolResult('first')], blocksB: [toolResult('second')] },
    { blocksA: [toolResult('first')], blocksB: [] },
  ].map(options => ({ operation: 'merge-content', ...options, universal: true }))],
  ['error tool results accept only text and preserve untouched error content', [
    { blocksA: [toolResult('error', true)], blocksB: [{ type: 'image', source: { type: 'url', url: 'fixture' } }] },
    { blocksA: [toolResult('error', true)], blocksB: [{ type: 'image', source: { type: 'url', url: 'fixture' } }, text(' explanation ')] },
    { blocksA: [toolResult([text(' original '), { type: 'image', source: { type: 'url', url: 'legacy' } }], true)], blocksB: [text(' incoming ')] },
    { blocksA: [toolResult()], blocksB: [{ type: 'document', source: { type: 'url', url: 'fixture' } }, text(' after ')] },
    { blocksA: [toolResult('\n\t')], blocksB: [{ type: 'image', source: { type: 'url', url: 'fixture' } }] },
  ].map(options => ({ operation: 'merge-content', ...options, universal: true }))],
  ['user tool results merge honors gate then hoists results ahead of user content', [false, true].map(universal => ({ operation: 'merge-user-tools', a: user([text('head'), toolResult(' output ')], true), b: user([text(' reminder '), toolResult('second')], false, 'second'), universal }))],
]
for (const [name, history, universal] of pairingCases) {
  fixtures.push([`tool pairing repair and strict policy: ${name}`, [false, true].map(strict => ({ operation: 'pairing', history, universal: universal === true, strict }))])
}

async function record(kind: 'typescript' | 'go', steps: Step[]): Promise<TraceEvent[]> {
  const home = await mkdtemp(join(tmpdir(), 'go-messages-parity-'))
  let child: ReturnType<typeof Bun.spawn> | undefined
  try {
    child = Bun.spawn(kind === 'go' ? [executable] : [process.execPath, '--no-env-file', join(rootDir, 'scripts/quality-gate/go-parity/messages.fixture.ts')], {
      cwd: home, env: createSandboxedTestEnvironment(home, { NODE_ENV: 'production', CLAUDE_CODE_SIMPLE: '1', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', ANTHROPIC_API_KEY: 'offline-fixture-key' }), stdin: new Blob([JSON.stringify({ homeDir: home, steps })]), stdout: 'pipe', stderr: 'pipe',
    })
    const timer = setTimeout(() => child?.kill('SIGKILL'), 15_000)
    try { const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()]); expect(code, stderr).toBe(0); return [{ kind: 'state', value: { decoded: JSON.parse(stdout), source: canonicalJsonSource(stdout) } }, { kind: 'stdio', value: { exitCode: code, stderr } }] } finally { clearTimeout(timer) }
  } finally { child?.kill(); if (child) await child.exited; await rm(home, { recursive: true, force: true }) }
}

for (const [name, steps] of fixtures) {
  test(`message factory actual TS self replay and Go parity: ${name}`, async () => {
    const expected = await record('typescript', steps)
    assertTraceEqual(expected, await record('typescript', steps))
    assertTraceEqual(expected, await record('go', steps))
  }, 40_000)
}
