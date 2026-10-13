import { describe, expect, test } from 'bun:test'
import { exposeSourceBindings } from './source-exports'

describe('actual source fixture exports', () => {
  test('adds only private bindings when upstream made another binding public', () => {
    const source = 'export function current() {}\nconst privateBinding = 1'
    const exposed = exposeSourceBindings(source, ['current', 'privateBinding', 'privateBinding'])
    expect(new Bun.Transpiler({ loader: 'ts' }).scan(exposed).exports.sort()).toEqual(['current', 'privateBinding'])
    expect(exposeSourceBindings(exposed, ['current', 'privateBinding'])).toBe(exposed)
  })

  test('keeps a local binding separately addressable when upstream aliases its export', () => {
    const source = 'const local = 1\nexport { local as publicName }'
    expect(new Bun.Transpiler({ loader: 'ts' }).scan(exposeSourceBindings(source, ['local'])).exports.sort()).toEqual(['local', 'publicName'])
  })
})
