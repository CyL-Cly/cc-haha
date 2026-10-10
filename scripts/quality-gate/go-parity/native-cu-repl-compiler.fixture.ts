import { assertDisposableFixtureHome } from './fixture-guard'
import { mock } from 'bun:test'
assertDisposableFixtureHome(process.env.HOME, 'Computer Use REPL compiler Source comparison')
if (process.env.CC_HAHA_CU_COMPILER_FIXTURE !== '1') throw new Error('Missing compiler fixture marker')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const originalCrypto = await import('node:crypto')
let uuids: string[] = []
let cursor = 0
mock.module('node:crypto', () => ({ ...originalCrypto, randomUUID: () => {
  if (cursor >= uuids.length) throw new Error('Private UUID fixture exhausted')
  return uuids[cursor++]
} }))
const { compileReplCell } = await import('../../../src/utils/computerUse/replCompiler')
const observations = []
for (const row of input.cases) {
  uuids = row.uuids
  cursor = 0
  try {
    const value = compileReplCell(row.code, row.prior ?? [])
    observations.push({ value: { bindings: value.bindings, warnings: value.warnings, source: Array.from({ length: value.source.length }, (_, index) => value.source.charCodeAt(index)) }, uuidCalls: cursor })
  } catch (error) {
    observations.push({ error: { name: (error as Error).name, message: (error as Error).message }, uuidCalls: cursor })
  }
}
process.stdout.write(JSON.stringify(observations))
