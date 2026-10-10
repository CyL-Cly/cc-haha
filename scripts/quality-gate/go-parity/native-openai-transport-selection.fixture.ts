import { assertDisposableFixtureHome } from './fixture-guard'
assertDisposableFixtureHome(process.env.HOME, 'Native OpenAI transport selection')
if (process.env.CC_HAHA_OPENAI_SELECTION_FIXTURE !== '1') throw new Error('Unowned transport selection fixture')
// Importing client.ts does not initialize a transport. No getAnthropicClient or
// token read is called; this fixture executes the original exported predicate.
const { shouldUseOpenAICodexTransport } = await import('../../../src/services/api/client')
const rows = JSON.parse(await new Response(Bun.stdin.stream()).text())
process.stdout.write(JSON.stringify(rows.map(shouldUseOpenAICodexTransport)))
