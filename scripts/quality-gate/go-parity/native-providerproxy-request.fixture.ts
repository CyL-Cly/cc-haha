import { assertDisposableFixtureHome } from './fixture-guard'

assertDisposableFixtureHome(process.env.HOME, 'Native provider request')
if (process.env.CC_HAHA_PROVIDER_REQUEST_FIXTURE !== '1') throw new Error('Unowned provider request fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const { anthropicToOpenaiResponses } = await import('../../../src/server/proxy/transform/anthropicToOpenaiResponses')
const { resolveRequestCompatibility } = await import('../../../src/server/proxy/transform/requestCompatibility')
const rows = input.rows.map((row: any) => {
  try {
    return { value: row.kind === 'compatibility'
      ? resolveRequestCompatibility(row.body, row.options)
      : anthropicToOpenaiResponses(row.body, row.options) }
  } catch (error) {
    const failure = error as Error & { type?: string, status?: number }
    return { error: failure.message, type: failure.type, status: failure.status }
  }
})
process.stdout.write(JSON.stringify(rows))
