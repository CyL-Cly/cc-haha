export type TraceEvent = {
  kind: 'model-request' | 'tool' | 'permission' | 'hook' | 'state' | 'transport' | 'persistence' | 'stdio'
  value: unknown
}

export type TraceDifference = { path: string; expected: unknown; actual: unknown }

const missing = Symbol('missing')

function difference(expected: unknown, actual: unknown, path: string): TraceDifference | undefined {
  if (Object.is(expected, actual)) return undefined
  if (Array.isArray(expected) && Array.isArray(actual)) {
    if (expected.length !== actual.length) return { path: `${path}.length`, expected: expected.length, actual: actual.length }
    for (let index = 0; index < expected.length; index++) {
      const found = difference(expected[index], actual[index], `${path}[${index}]`)
      if (found) return found
    }
    return undefined
  }
  if (expected !== null && actual !== null && typeof expected === 'object' && typeof actual === 'object' && !Array.isArray(expected) && !Array.isArray(actual)) {
    const expectedObject = expected as Record<string, unknown>
    const actualObject = actual as Record<string, unknown>
    for (const key of [...new Set([...Object.keys(expectedObject), ...Object.keys(actualObject)])].sort()) {
      const left = Object.hasOwn(expectedObject, key) ? expectedObject[key] : missing
      const right = Object.hasOwn(actualObject, key) ? actualObject[key] : missing
      if (left === missing || right === missing) return { path: `${path}.${key}`, expected: left === missing ? '<missing>' : left, actual: right === missing ? '<missing>' : right }
      const found = difference(left, right, `${path}.${key}`)
      if (found) return found
    }
    return undefined
  }
  return { path, expected, actual }
}

/** No text, array, error, budget, or null/missing normalization is implicit. */
export function firstTraceDifference(expected: readonly TraceEvent[], actual: readonly TraceEvent[]): TraceDifference | undefined {
  return difference(expected, actual, '$')
}

export function assertTraceEqual(expected: readonly TraceEvent[], actual: readonly TraceEvent[]) {
  const found = firstTraceDifference(expected, actual)
  if (found) throw new Error(`First semantic difference at ${found.path}: expected ${JSON.stringify(found.expected)}, actual ${JSON.stringify(found.actual)}`)
}

class JsonNumber {
  constructor(readonly source: string) {}
}

function canonicalNumber(source: string) {
  const match = /^(-?)(\d+)(?:\.(\d+))?(?:[eE]([+-]?\d+))?$/.exec(source)
  if (!match) throw new Error('Invalid JSON number source')
  let digits = `${match[2]}${match[3] ?? ''}`.replace(/^0+/, '')
  let exponent = BigInt(match[4] ?? '0') - BigInt((match[3] ?? '').length)
  if (!digits) return `${match[1]}0`
  while (digits.endsWith('0')) {
    digits = digits.slice(0, -1)
    exponent++
  }
  return `${match[1]}${digits}e${exponent}`
}

/**
 * JSON.parse alone rounds numbers above 2^53 and can hide a migration defect.
 * The pinned Bun exposes reviver source text; fail if that evidence is absent.
 * Compare mathematical JSON numbers without collapsing distinct integers,
 * while keeping strings, array order, object fields, and negative zero intact.
 */
export function canonicalJsonSource(source: string): string {
  const parse = JSON.parse as (text: string, reviver: (key: string, value: unknown, context?: { source?: string }) => unknown) => unknown
  const parsed = parse(source, (_key, value, context) => {
    if (typeof value !== 'number') return value
    if (!context?.source) throw new Error('JSON number source evidence is unavailable')
    return new JsonNumber(canonicalNumber(context.source))
  })
  function project(value: unknown): unknown {
    if (value instanceof JsonNumber) return ['number', value.source]
    if (value === null) return ['null']
    if (Array.isArray(value)) return ['array', value.map(project)]
    if (typeof value === 'object') return ['object', Object.entries(value as Record<string, unknown>).sort(([left], [right]) => left < right ? -1 : left > right ? 1 : 0).map(([key, entry]) => [key, project(entry)])]
    return [typeof value, value]
  }
  return JSON.stringify(project(parsed))
}
