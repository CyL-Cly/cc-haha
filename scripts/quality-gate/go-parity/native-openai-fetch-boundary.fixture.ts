import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'

assertDisposableFixtureHome(process.env.HOME, 'OpenAI fetch boundary')
if (process.env.CC_HAHA_OPENAI_FETCH_BOUNDARY_FIXTURE !== '1') throw new Error('Unowned OpenAI fetch boundary fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const { buildOpenAICodexFetch } = await import('../../../src/services/openaiAuth/fetch')
const { OpenAICodexTurnState } = await import('../../../src/services/openaiAuth/turnState')
const tokenPath = join(process.env.CLAUDE_CONFIG_DIR!, 'private-openai-tokens.json')
mkdirSync(process.env.CLAUDE_CONFIG_DIR!, { recursive: true })
process.env.OPENAI_CODEX_OAUTH_FILE = tokenPath
process.env.CC_HAHA_OPENAI_REQUEST_COMPRESSION = 'off'
Date.now = () => 1700000000000
const output = []
for (const row of input.rows) {
  writeFileSync(tokenPath, JSON.stringify(row.tokens), { mode: 0o600 })
  const controller = new AbortController()
  const state = new OpenAICodexTurnState(controller.signal)
  if (row.turnState) state.capture(row.turnState)
  let request: unknown = null
  const fetcher = buildOpenAICodexFetch(async (url, init) => {
    request = { endpoint: String(url), body: JSON.parse(Buffer.from(init!.body as Uint8Array).toString()), headers: Object.fromEntries(new Headers(init!.headers)) }
    return new Response(row.upstreamBody, { status: row.upstreamStatus, headers: row.upstreamHeaders })
  }, 'fixture', state, row.agent)
  let result: unknown
  try {
    const response = await fetcher!('https://anthropic.invalid/v1/messages', { method: 'POST', headers: { 'x-claude-code-session-id': row.session }, body: JSON.stringify(row.body), signal: controller.signal })
    const status = response.status
    const headers = Object.fromEntries(response.headers)
    try { result = { status, headers, body: await response.text(), request, turnState: state.get() ?? null } } catch (error) {
      result = { status, headers, readError: (error as Error).message, request, turnState: state.get() ?? null }
    }
  } catch (error) {
    result = { error: (error as Error).message, request, turnState: state.get() ?? null }
  }
  state[Symbol.dispose]()
  output.push(result)
}
process.stdout.write(JSON.stringify(output))
