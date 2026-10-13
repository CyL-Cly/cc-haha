import { spyOn } from 'bun:test'
import { mkdir, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'
const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(process.env.HOME, 'Windows error UTF16')
if (process.env.CC_HAHA_WINDOWS_ERROR_FIXTURE !== '1') throw new Error('Unowned Windows error fixture')
Object.defineProperty(process, 'platform', { value: 'win32' })
const runtime = join(process.env.CLAUDE_CONFIG_DIR!, '.runtime')
await mkdir(join(runtime, 'venv', 'Scripts'), { recursive: true })
await writeFile(join(runtime, 'venv', 'Scripts', 'python.exe'), '')
await writeFile(join(runtime, 'venv', 'Scripts', 'pip.exe'), '')
const exec = await import('../../../../src/utils/execFileNoThrow')
let active: any
spyOn(exec, 'execFileNoThrow').mockImplementation(async (_file: string, args: string[]) => {
  if (args[0]?.endsWith('win_helper.py')) {
    if (args[1] === active.command) return { code: 0, stdout: JSON.stringify(active.envelope), stderr: '' } as any
    return { code: 0, stdout: JSON.stringify({ ok: true, result: null }), stderr: '' } as any
  }
  return { code: 0, stdout: '', stderr: '' } as any
})
const bridge = await import('../../../../src/utils/computerUse/pythonBridge')
const helper = await import('../../../../src/utils/computerUse/helperBridge')
spyOn(helper, 'callHelper').mockImplementation((command: string, payload: any) => bridge.callPythonHelper(command, payload))
const { createCliExecutor } = await import('../../../../src/utils/computerUse/executor')
const { bindSessionContext } = await import('../../../../src/vendor/computer-use-mcp/mcpServer')
const results = []
for (active of input.rows) {
  let bridgeMessage = ''
  try { await bridge.callPythonHelper(active.command, {}) } catch (error) { bridgeMessage = (error as Error).message }
  const dispatch = bindSessionContext({ serverName: 'computer-use', logger: { debug() {}, warn() {}, error() {}, info() {}, silly() {} }, executor: createCliExecutor(), isDisabled: () => false, ensureOsPermissions: async () => ({ granted: true, accessibility: true, screenRecording: true }), getSubGates: () => ({ pixelValidation: false, clipboardPasteMultiline: true, mouseAnimation: true, hideBeforeAction: true, autoTargetDisplay: true, clipboardGuard: true }), cropRawPatch: () => null } as any, 'pixels', {
    getAllowedApps: () => [], getGrantFlags: () => ({ clipboardRead: true, clipboardWrite: true, systemKeyCombos: true }), getUserDeniedBundleIds: () => new Set(), getSelectedDisplayId: () => undefined, getDisplayPinnedByModel: () => false, getDisplayResolvedForApps: () => undefined, onResolvedDisplayUpdated() {}, onDisplayPinned() {}, onDisplayResolvedForApps() {},
  } as any)
  results.push({ bridgeMessage, result: await dispatch(active.tool, active.args, new AbortController().signal) })
}
process.stdout.write(JSON.stringify(results) + '\n')
