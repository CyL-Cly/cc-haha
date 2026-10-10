import { mock } from 'bun:test'

const root = Bun.argv[2]
const { assertDisposableFixtureHome } = await import(root + '/scripts/quality-gate/go-parity/fixture-guard.ts')
assertDisposableFixtureHome(process.env.HOME, 'CU handle actual Source bridge replay')
if (process.env.CC_HAHA_CU_WORKER_FIXTURE !== '1') throw new Error('Unowned CU bridge replay')
const input = await Bun.stdin.json()
const native = await import(root + '/src/vendor/computer-use-mcp/nativeError.ts')

// Replace only the native transport. The original guarded bridge and isolated
// Source worker process own argument parsing, state caching and error handling.
async function helper(command: string, payload: unknown = {}) {
  const response = await fetch(input.peerURL, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ command, payload }),
  })
  const value = await response.json() as any
  if (!value.ok) throw new native.NativeCommandError(value.error, value.code)
  return value.result
}
mock.module(root + '/src/utils/computerUse/helperBridge.ts', () => ({ callHelper: helper }))
const executor = await import(root + '/src/utils/computerUse/executor.ts')
const calls = await import(root + '/src/vendor/computer-use-mcp/toolCalls.ts')
const server = await import(root + '/src/vendor/computer-use-mcp/mcpServer.ts')
const { ComputerUseRepl } = await import(root + '/src/utils/computerUse/replRuntime.ts')
calls._test.resetDeliveredAppInstructions()
let held = false
const adapter = {
  serverName: 'computer-use',
  logger: { debug() {}, silly() {}, error() {} },
  isDisabled: () => false,
  ensureOsPermissions: async () => {
    const result = await helper('check_permissions')
    return { granted: result.accessibility && result.screenRecording !== false }
  },
  executor: { capabilities: { platform: 'darwin' }, engine: executor.createCodexEngine() },
}
const context = {
  getAllowedApps: () => [],
  getGrantFlags: () => ({ systemKeyCombos: false }),
  getUserDeniedBundleIds: () => [],
  getSelectedDisplayId: () => undefined,
  checkCuLock: async () => ({ holder: held ? 'fixture-session' : undefined, isSelf: held }),
  acquireCuLock: async () => { held = true },
}
const bind = server.bindSessionContext(adapter, 'pixels', context)
const invoke = async (name: string, args: unknown) => {
  const result = await bind(name, args)
  return {
    ...result,
    ...(result[calls.RESOLVED_APP_PATH] === undefined ? {} : { app: result[calls.RESOLVED_APP_PATH] }),
    ...(result[native.NATIVE_ERROR] === undefined ? {} : { nativeError: result[native.NATIVE_ERROR] }),
  }
}
const runtime = new ComputerUseRepl()
const output = []
try {
  for (const code of input.cells) {
    const result = await runtime.run({ code, timeoutMs: 10000 }, invoke)
    output.push({ isError: result.isError === true, content: result.content })
  }
} finally {
  await runtime.reset()
}
process.stdout.write(JSON.stringify(output) + '\n')
