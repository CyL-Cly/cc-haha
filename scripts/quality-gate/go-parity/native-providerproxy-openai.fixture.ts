import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'

assertDisposableFixtureHome(process.env.HOME, 'Native OpenAI request')
if (process.env.CC_HAHA_OPENAI_REQUEST_FIXTURE !== '1') throw new Error('Unowned OpenAI request fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const models = await import('../../../src/services/openaiAuth/models')
const { resolveOpenAIRequestIdentity } = await import('../../../src/services/openaiAuth/requestIdentity')
const { resolvePromptCacheKey } = await import('../../../src/server/proxy/promptCacheKey')
const { getOpenAIPolicyError } = await import('../../../src/services/openaiAuth/policyError')
const { OpenAICodexTurnState } = await import('../../../src/services/openaiAuth/turnState')
const streamPolicy = await import('../../../src/services/openaiAuth/streamPolicy')
const { buildOpenAICodexFetch } = await import('../../../src/services/openaiAuth/fetch')
const tokenPath = join(process.env.CLAUDE_CONFIG_DIR!, 'private-openai-tokens.json')
mkdirSync(process.env.CLAUDE_CONFIG_DIR!, { recursive: true })
process.env.OPENAI_CODEX_OAUTH_FILE = tokenPath
process.env.CC_HAHA_OPENAI_REQUEST_COMPRESSION = 'off'
const envKeys = ['OPENAI_CODEX_MODEL', 'OPENAI_CODEX_HAIKU_MODEL', 'OPENAI_CODEX_SONNET_MODEL', 'OPENAI_CODEX_OPUS_MODEL', 'CC_HAHA_OPENAI_REASONING_EFFORT']
const output = []
for (const row of input.rows) {
  for (const key of envKeys) delete process.env[key]
  Object.assign(process.env, row.env ?? {})
  if (row.kind === 'models') {
    output.push({
      model: models.resolveOpenAICodexModel(row.model),
      responses: models.isOpenAIResponsesModel(row.model),
      display: models.getOpenAIModelDisplayName(row.model),
      context: models.getOpenAICodexContextWindowForModel(row.model),
      effort: models.resolveOpenAIReasoningEffortWithPriority(row.model, row.efforts),
      entry: models.getOpenAIModelCatalogEntry(row.model) ?? null,
    })
  } else if (row.kind === 'identity') {
    output.push({ identity: resolveOpenAIRequestIdentity(row.session, row.agent) ?? null, cacheKey: resolvePromptCacheKey(row.body, row.session) ?? null })
  } else if (row.kind === 'policy') {
    output.push(getOpenAIPolicyError(row.input))
  } else if (row.kind === 'streamPolicy') {
    const response = row.marker === null ? undefined : new Response(null, { headers: { [streamPolicy.OPENAI_CODEX_STREAM_MARKER_HEADER]: row.marker } })
    output.push({ timeout: streamPolicy.resolveOpenAICodexFirstTokenTimeoutMs(response, row.fallback, row.override), retry: streamPolicy.canRetryOpenAICodexStreamWithBufferedContent(response, row.crossed) })
  } else if (row.kind === 'turnState') {
    const controller = new AbortController()
    if (row.aborted) controller.abort()
    const state = new OpenAICodexTurnState(controller.signal)
    const observations = []
    for (const action of row.actions) {
      if (action.kind === 'capture') state.capture(action.value)
      if (action.kind === 'abort') controller.abort()
      if (action.kind === 'dispose') state[Symbol.dispose]()
      observations.push(state.get() ?? null)
    }
    state[Symbol.dispose]()
    output.push(observations)
  } else if (row.kind === 'request') {
    writeFileSync(tokenPath, JSON.stringify(row.tokens), { mode: 0o600 })
    const controller = new AbortController()
    const state = new OpenAICodexTurnState(controller.signal)
    if (row.turnState) state.capture(row.turnState)
    let captured: unknown = null
    const fetcher = buildOpenAICodexFetch(async (url, init) => {
      captured = { endpoint: String(url), body: JSON.parse(String(init!.body)), headers: Object.fromEntries(new Headers(init!.headers)) }
      return Response.json({ id: 'fixture-response', status: 'completed', output: [], usage: { input_tokens: 0, output_tokens: 0 } }, { status: 200, headers: { 'x-codex-turn-state': 'fixture-routing-state' } })
    }, 'fixture', state, row.agent)
    try {
      await fetcher!('https://anthropic.invalid/v1/messages', { method: 'POST', headers: row.session ? { 'x-claude-code-session-id': row.session } : {}, body: JSON.stringify(row.body), signal: controller.signal })
      output.push({ request: captured })
    } catch (error) {
      output.push({ error: (error as Error).message })
    } finally {
      state[Symbol.dispose]()
    }
  }
}
process.stdout.write(JSON.stringify({ catalog: models.OPENAI_CODEX_MODEL_CATALOG, rows: output }))
