import { readFileSync } from 'node:fs'
import { parse } from 'acorn'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Automatic OpenAI question physical HTTP')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths(), loopbackFetchOrigin: input.endpoint })
await import('../../../preload')
const { anthropicToOpenaiResponses } = await import('../../../src/server/proxy/transform/anthropicToOpenaiResponses')
const { resolveOpenAICodexModel } = await import('../../../src/services/openaiAuth/models')
const { openaiResponsesStreamToAnthropicResponse } = await import('../../../src/server/proxy/streaming/openaiResponsesStreamToAnthropicResponse')
const { getNetworkProxyFetchOptions } = await import('../../../src/server/services/networkSettings')
const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(readFileSync(new URL('../../../src/server/services/autoQuestionDecisionService.ts', import.meta.url), 'utf8'))
const ast = parse(source, { ecmaVersion: 'latest', sourceType: 'module' }) as any
const node = ast.body.find((node: any) => node.type === 'FunctionDeclaration' && node.id?.name === 'askOpenAIOfficial')
if (!node) throw new Error('Missing actual automatic OpenAI request producer')
// Replace only the external endpoint and resolved credential primitive. The
// original request conversion, physical fetch and stream collector all run.
const api = new Function('anthropicToOpenaiResponses', 'resolveOpenAICodexModel', 'openaiResponsesStreamToAnthropicResponse', 'hahaOpenAIOAuthService', 'getNetworkProxyFetchOptions', 'OPENAI_CODEX_API_ENDPOINT', 'MAX_OUTPUT_TOKENS', `${source.slice(node.start, node.end)}\nreturn askOpenAIOfficial`)(anthropicToOpenaiResponses, resolveOpenAICodexModel, openaiResponsesStreamToAnthropicResponse, { ensureFreshTokens: async () => input.tokens }, getNetworkProxyFetchOptions, input.endpoint, 512)
try {
  const answer = await api(input.body, input.body.model, { proxy: { mode: 'direct' }, aiRequestTimeoutMs: 15_000 }, new AbortController().signal)
  process.stdout.write(JSON.stringify({ answer, capabilities }))
} catch (error) {
  process.stdout.write(JSON.stringify({ error: { name: (error as Error).name, message: (error as Error).message }, capabilities }))
}
