import { assertDisposableFixtureHome } from './fixture-guard'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'compact prompt')
if (process.env.CC_HAHA_NATIVE_COMPACT_PROMPT_FIXTURE !== '1') throw new Error('compact prompt fixture guard missing')
const prompt = await import('../../../src/services/compact/prompt')
const rows = input.rows.map((row: any) => ({
  prompt: row.direction ? prompt.getPartialCompactPrompt(row.custom, row.direction) : prompt.getCompactPrompt(row.custom, row.arm),
  formatted: prompt.formatCompactSummary(row.summary),
  user: prompt.getCompactUserSummaryMessage(row.summary, row.suppress, row.path, row.recent),
}))
process.stdout.write(JSON.stringify({ rows }))
