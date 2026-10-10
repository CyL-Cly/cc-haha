import { assertDisposableFixtureHome } from './fixture-guard'
import { mock } from 'bun:test'
assertDisposableFixtureHome(process.env.HOME, 'OpenAI auth presence')
if (process.env.CC_HAHA_OPENAI_HAS_AUTH_FIXTURE !== '1') throw new Error('Unowned auth presence fixture')
const rows = JSON.parse(await new Response(Bun.stdin.stream()).text())
let value: any
const storage = await import('../../../src/services/openaiAuth/storage')
mock.module('../../../src/services/openaiAuth/storage', () => ({ ...storage, getOpenAIOAuthTokens: () => value }))
const { shouldUseOpenAICodexAuth } = await import('../../../src/services/openaiAuth/fetch')
process.stdout.write(JSON.stringify(rows.map((row: any) => { value = row; return shouldUseOpenAICodexAuth() })))
