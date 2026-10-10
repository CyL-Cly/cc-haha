import { expect, test } from 'bun:test'
import { assertTraceEqual, canonicalJsonSource, firstTraceDifference, type TraceEvent } from './trace'

const baseline: TraceEvent[] = [
  { kind: 'model-request', value: { model: 'fixture', system: 'Exact prompt', messages: [{ role: 'user', content: '中文' }], tools: [{ name: 'Write', input_schema: { required: ['path', 'content'] } }], max_tokens: 4096, stop_sequence: null } },
  { kind: 'permission', value: { behavior: 'allow', updatedInput: { path: '/fixture', content: 'text' } } },
  { kind: 'tool', value: { id: 'call_1', executions: 1, error: 'InputValidationError: exact feedback', result: 'written' } },
  { kind: 'state', value: { status: 'idle', queue: ['first', 'second'], cost: 0.01 } },
  { kind: 'persistence', value: { messages: ['prior', 'next'] } },
  { kind: 'stdio', value: { stdout: 'result\n', stderr: '', exitCode: 0 } },
]

test('self replay is identical and only JSON object property order is immaterial', () => {
  expect(firstTraceDifference(baseline, structuredClone(baseline))).toBeUndefined()
  const reordered = structuredClone(baseline)
  const value = reordered[0]!.value as Record<string, unknown>
  reordered[0]!.value = Object.fromEntries(Object.entries(value).reverse())
  expect(() => assertTraceEqual(baseline, reordered)).not.toThrow()
})

test('mutations to each observable semantic axis fail at the first divergence', () => {
  const mutations: Array<{ index: number; mutate(value: Record<string, any>): void; path: string }> = [
    { index: 0, mutate: value => { value.system += ' ' }, path: '$[0].value.system' },
    { index: 0, mutate: value => { value.max_tokens++ }, path: '$[0].value.max_tokens' },
    { index: 0, mutate: value => { value.tools[0].input_schema.required.reverse() }, path: '$[0].value.tools[0].input_schema.required[0]' },
    { index: 0, mutate: value => { delete value.stop_sequence }, path: '$[0].value.stop_sequence' },
    { index: 1, mutate: value => { value.behavior = 'deny' }, path: '$[1].value.behavior' },
    { index: 1, mutate: value => { value.updatedInput.content = 'modified' }, path: '$[1].value.updatedInput.content' },
    { index: 2, mutate: value => { value.executions++ }, path: '$[2].value.executions' },
    { index: 2, mutate: value => { value.error = 'different error' }, path: '$[2].value.error' },
    { index: 3, mutate: value => { value.queue.reverse() }, path: '$[3].value.queue[0]' },
    { index: 3, mutate: value => { value.status = 'running' }, path: '$[3].value.status' },
    { index: 4, mutate: value => { value.messages.pop() }, path: '$[4].value.messages.length' },
    { index: 5, mutate: value => { value.stderr = 'diagnostic\n' }, path: '$[5].value.stderr' },
    { index: 5, mutate: value => { value.exitCode = 1 }, path: '$[5].value.exitCode' },
  ]
  for (const { index, mutate, path } of mutations) {
    const changed = structuredClone(baseline)
    mutate(changed[index]!.value as Record<string, any>)
    expect(firstTraceDifference(baseline, changed)?.path).toBe(path)
    expect(() => assertTraceEqual(baseline, changed)).toThrow('First semantic difference')
  }
  expect(firstTraceDifference(baseline, baseline.slice(1))?.path).toBe('$.length')
})

test('missing, undefined, null, and changed types remain distinct', () => {
  const trace = (value: unknown): TraceEvent[] => [{ kind: 'state', value }]
  for (const [left, right] of [[{}, { field: undefined }], [{ field: null }, { field: undefined }], [[], {}], [0, '0'], [false, null]]) {
    expect(firstTraceDifference(trace(left), trace(right))).toBeDefined()
  }
  expect(firstTraceDifference(trace(null), trace(null))).toBeUndefined()
})

test('wire comparison detects numbers hidden by the JavaScript double parser', () => {
  expect(JSON.parse('9007199254740992')).toBe(JSON.parse('9007199254740993'))
  expect(canonicalJsonSource('9007199254740992')).not.toBe(canonicalJsonSource('9007199254740993'))
  expect(canonicalJsonSource('{"n":1.2300e3,"a":[null,true,"1230"]}')).toBe(canonicalJsonSource('{"a":[null,true,"1230"],"n":1230}'))
  expect(canonicalJsonSource('1e99999')).not.toBe(canonicalJsonSource('2e99999'))
  expect(canonicalJsonSource('-0')).not.toBe(canonicalJsonSource('0'))
  expect(canonicalJsonSource('1')).not.toBe(canonicalJsonSource('"1"'))
  expect(() => canonicalJsonSource('not JSON')).toThrow()
})
