import { spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'

assertDisposableFixtureHome(process.env.HOME, 'Windows pixel Computer Use')
if (process.env.CC_HAHA_CU_WINDOWS_PIXEL_FIXTURE !== '1') throw new Error('Unowned Windows pixel fixture')
const input = await Bun.stdin.json()
Object.defineProperty(process, 'platform', { value: 'win32' })
const helper = await import('../../../src/utils/computerUse/helperBridge')
let current: any
let activeRow: any
let activeAbort: AbortController | undefined
let calls: any[] = []
const counters: Record<string, number> = {}
spyOn(helper, 'callHelper').mockImplementation(async (command: string, payload: any) => {
  calls.push({ command, payload })
  if (activeRow?.abortOn === command) activeAbort?.abort()
  const count = counters[command] = (counters[command] ?? 0) + 1
  const replies = current.replies ?? {}
  if (Object.hasOwn(replies, command)) {
    const selected = Array.isArray(replies[command]) ? replies[command][Math.min(count - 1, replies[command].length - 1)] : replies[command]
    if (selected?.throw) throw new Error(selected.throw)
    return selected
  }
  if (command === 'frontmost_app') return { bundleId: 'owned.fixture', displayName: 'Owned fixture', pid: 1234 }
  if (command === 'get_display_size') return { width: 1920, height: 1080, scaleFactor: 1, originX: 0, originY: 0, displayId: 1, label: 'Primary' }
  if (command === 'list_displays') return [{ width: 1920, height: 1080, scaleFactor: 1, originX: 0, originY: 0, displayId: 1, label: 'Primary' }]
  if (command === 'cursor_position') return { x: 480, y: 270 }
  if (command === 'list_installed_apps') return [{ bundleId: 'owned.fixture', displayName: 'Owned fixture' }]
  if (command === 'read_clipboard') return 'owned original clipboard'
  if (command === 'prepare_for_action') return []
  if (command === 'screenshot' || command === 'resolve_prepare_capture') return { base64: Buffer.alloc(1025, 1).toString('base64'), width: payload.targetWidth, height: payload.targetHeight, displayWidth: 1920, displayHeight: 1080, originX: 0, originY: 0, displayId: 1, display: { width: 1920, height: 1080, scaleFactor: 1, originX: 0, originY: 0, displayId: 1, label: 'Primary' } }
  if (command === 'zoom') return { base64: Buffer.alloc(1025, 2).toString('base64'), width: payload.targetWidth, height: payload.targetHeight }
  return null
})
const { createCliExecutor } = await import('../../../src/utils/computerUse/executor')
const { bindSessionContext, buildPlatformComputerUseTools } = await import('../../../src/vendor/computer-use-mcp/mcpServer')
const defaults = { pixelValidation: false, clipboardPasteMultiline: true, mouseAnimation: true, hideBeforeAction: true, autoTargetDisplay: true, clipboardGuard: true }
const { resetMouseButtonHeld } = await import('../../../src/vendor/computer-use-mcp/windowsLegacyToolCalls')
const { NATIVE_CALL_NOT_DISPATCHED } = await import('../../../src/vendor/computer-use-mcp/toolCalls')
const profiles = []
for (current of input.profiles ?? []) {
  resetMouseButtonHeld()
  calls = []
  for (const key of Object.keys(counters)) delete counters[key]
  let selected: number | undefined, pinned = false, resolved: string | undefined
  const executor = createCliExecutor({ getMouseAnimationEnabled: () => current.gates?.mouseAnimation ?? true, getHideBeforeActionEnabled: () => current.gates?.hideBeforeAction ?? true })
  const dispatch = bindSessionContext({ serverName: 'computer-use', logger: { debug() {}, warn() {}, error() {}, info() {}, silly() {} }, executor, isDisabled: () => current.disabled === true, ensureOsPermissions: async () => ({ granted: true, accessibility: true, screenRecording: true }), getSubGates: () => ({ ...defaults, ...current.gates }), cropRawPatch: () => null } as any, current.mode ?? 'pixels', {
    getAllowedApps: () => [], getGrantFlags: () => ({ clipboardRead: true, clipboardWrite: true, systemKeyCombos: true }), getUserDeniedBundleIds: () => new Set(),
    getSelectedDisplayId: () => selected, getDisplayPinnedByModel: () => pinned, getDisplayResolvedForApps: () => resolved,
    onResolvedDisplayUpdated: (id: number) => { selected = id }, onDisplayPinned: (id?: number) => { selected = id; pinned = id !== undefined }, onDisplayResolvedForApps: (key: string) => { resolved = key },
  } as any)
  const results = []
  for (const row of current.cases) {
    activeRow = row
    activeAbort = new AbortController()
    if (row.abortBefore) activeAbort.abort()
    const result = await dispatch(row.name, row.args, activeAbort.signal)
    results.push({ ...result, ...(result[NATIVE_CALL_NOT_DISPATCHED] ? { nativeCallNotDispatched: true } : {}) })
  }
  profiles.push({ results, calls, selected: selected ?? null, pinned })
}
const definitions = Object.fromEntries(['pixels', 'normalized_0_100'].map(mode => [mode, buildPlatformComputerUseTools({ platform: 'win32', screenshotFiltering: 'none' }, mode as any)]))
const { getComputerUsePrompt, getComputerUseToolAllowlist } = await import('../../../src/skills/bundled/computerUse')
process.stdout.write(JSON.stringify({ definitions, profiles, skill: { prompt: getComputerUsePrompt('win32'), allowedTools: getComputerUseToolAllowlist('win32') } }))
