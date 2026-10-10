import fs from 'node:fs'
import { fileURLToPath } from 'node:url'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_SDK_MCP_REGISTRATION_FIXTURE !== '1') throw new Error('Explicit pinned SDK registration fixture required')
assertDisposableFixtureHome(process.env.HOME, 'SDK MCP registration')
const source = fs.readFileSync(fs.realpathSync(fileURLToPath(new URL('../../src/cli/print.ts', import.meta.url))), 'utf8')
const start = source.indexOf('          if (\n            message.request.sdkMcpServers &&')
const end = source.indexOf('\n          await handleInitializeRequest(', start)
if (start < 0 || end < 0) throw new Error('Original initialize SDK configuration producer missing')
// Execute exactly the original outer input-loop registration producer. The
// isolated closure state is the same ordinary object used by print.ts.
const body = new Bun.Transpiler({ loader: 'ts' }).transformSync(source.slice(start, end))
const register = new Function('sdkMcpConfigs', 'message', body)
const input = await Bun.stdin.json()
const rows = input.rows.map((names: unknown) => {
  const configs: Record<string, unknown> = {}
  let error: string | undefined
  try { register(configs, { request: { sdkMcpServers: names } }) } catch (failure: any) { error = failure.message }
  return { configs, ...(error !== undefined ? { error } : {}) }
})
process.stdout.write(JSON.stringify(rows) + '\n')
