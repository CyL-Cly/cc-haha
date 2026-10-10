import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../scripts/quality-gate/go-parity/client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(process.env.HOME, 'Claude actual client')
// Use the original plaintext credential primitive in the isolated Source child.
// The managed env token remains authoritative; no system Keychain is accessed.
Object.defineProperty(process, 'platform', { value: 'linux' })
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: [...absentManagedSettingsFixturePaths(), '/home/claude/.claude/remote/.api_key', '/lib/libc.musl-aarch64.so.1', '/proc/version'], loopbackFetchOrigin: input.origin })
await import('../../preload')
const bootstrap = await import('../../src/bootstrap/state')
bootstrap.setCwdState(home)
bootstrap.setOriginalCwd(home)
bootstrap.setProjectRoot(home)
bootstrap.setIsInteractive(false)
bootstrap.switchSession('00000000-0000-4000-8000-000000000000')
const { enableConfigs } = await import('../../src/utils/config')
enableConfigs()
const { getAnthropicClient } = await import('../../src/services/api/client')
const { getAllModelBetas } = await import('../../src/utils/betas')
const observations = []
for (const model of input.models) {
  const betas = getAllModelBetas(model)
  let captured: any
  const client = await getAnthropicClient({ model, source: 'sdk', maxRetries: 0, fetchOverride: async (url, init) => {
    const target = new URL(String(url))
    if (target.origin !== 'https://api.anthropic.com') throw new Error('Unexpected Source authority')
    captured = { authority: target.origin, path: target.pathname, headers: [...new Headers(init?.headers)], body: JSON.parse(String(init?.body)) }
    // Only the external network primitive changes. Original getAnthropicClient,
    // beta SDK construction and response stream execute unmodified.
    return fetch(input.origin + target.pathname, init)
  } })
  const stream = await client.beta.messages.create({ model, messages: [{ role: 'user', content: 'Private actual Claude client' }], max_tokens: 32, stream: true, betas })
  for await (const event of stream) {}
  observations.push({ captured, betas })
}
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ observations, capabilities }), () => resolve()))
process.exit(0)
