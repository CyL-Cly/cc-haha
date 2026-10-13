import { assertDisposableFixtureHome } from './fixture-guard'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'CLI prefill original producer')
if (process.env.CC_HAHA_CLI_PREFILL_FIXTURE !== '1') throw new Error('Missing CLI prefill boundary')
const original = await import('../../../src/utils/earlyInput')
const rows = input.rows.map((text: string) => {
  original.seedEarlyInput(text)
  const hadInput = original.hasEarlyInput()
  const consumed = original.consumeEarlyInput()
  return { hadInput, consumed, secondConsume: original.consumeEarlyInput(), capturing: original.isCapturingEarlyInput() }
})
process.stdout.write(JSON.stringify({ rows }))
