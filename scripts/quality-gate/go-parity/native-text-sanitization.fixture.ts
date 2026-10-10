import { assertDisposableFixtureHome } from './fixture-guard'
import { createHash } from 'node:crypto'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'

assertDisposableFixtureHome(process.env.HOME, 'Unicode sanitization Source comparison')
if (process.env.CC_HAHA_TEXT_SANITIZATION_FIXTURE !== '1') throw new Error('Missing isolated sanitization marker')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const { partiallySanitizeUnicode, recursivelySanitizeUnicode } = await import('../../../src/utils/sanitization')
const digest = createHash('sha256')
const comparisons = input.debug ? readFileSync(join(process.env.HOME!, 'go-unicode.jsonl'), 'utf8').trimEnd().split('\n') : undefined
const differences: unknown[] = []
// Exhaustive single-codepoint comparison retains each UTF-16 JSON result and
// its delimiter in the digest. It includes unpaired surrogate code units.
for (let code = 0; code <= 0x10ffff; code++) {
  const value = JSON.stringify(partiallySanitizeUnicode(String.fromCodePoint(code)))
  digest.update(`${value}\n`)
  if (comparisons && comparisons[code] !== value && differences.length < 20) differences.push({ code: code.toString(16), go: comparisons[code], source: value })
}
process.stdout.write(JSON.stringify({ unicode: process.versions.unicode, digest: digest.digest('hex'), values: (input.debug ? input.values : input).map((value: unknown) => recursivelySanitizeUnicode(value)), differences }))
