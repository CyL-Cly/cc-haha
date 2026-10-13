import { mock } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await Bun.stdin.text())
const home = assertDisposableFixtureHome(input.homeDir, 'Dream detail UI Source')
if (process.env.CC_HAHA_DREAM_DETAIL_FIXTURE !== '1') throw new Error('Missing Dream detail Source fixture')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const React = await import('react')
let confirm: Record<string, () => void> = {}
mock.module('react', () => ({ ...React, default: React.default, useCallback: (fn: unknown) => fn, useSyncExternalStore: (_subscribe: unknown, get: () => unknown) => get() }))
mock.module('react/compiler-runtime', () => ({ c: (length: number) => Array(length).fill(Symbol.for('react.memo_cache_sentinel')) }))
const keybindings = await import('../../../src/keybindings/useKeybinding')
mock.module('../../../src/keybindings/useKeybinding', () => ({ ...keybindings, useKeybindings: (bindings: Record<string, () => void>) => { confirm = bindings } }))
const { DreamDetailDialog } = await import('../../../src/components/tasks/DreamDetailDialog')
const { formatDuration } = await import('../../../src/utils/format')
const text = (node: any): string => node === undefined || node === null || typeof node === 'boolean' ? '' : typeof node === 'string' || typeof node === 'number' ? String(node) : Array.isArray(node) ? node.map(text).join('') : text(node.props?.children)
const actualNow = Date.now
const rows = []
try {
  Date.now = () => input.now
  for (const task of input.tasks) {
    const calls: string[] = []
    const view: any = DreamDetailDialog({ task, onDone: () => calls.push('done'), onBack: () => calls.push('back'), onKill: () => calls.push('kill') })
    const dialog = view.props.children
    const keys = []
    for (const key of [' ', 'left', 'x', 'enter', 'escape', 'y', 'a']) {
      let prevented = false
      const start = calls.length
      view.props.onKeyDown({ key, preventDefault: () => { prevented = true } })
      if (key === 'enter' || key === 'y') confirm['confirm:yes']()
      if (key === 'escape') dialog.props.onCancel()
      keys.push({ key, prevented, calls: calls.slice(start) })
    }
    rows.push({ title: dialog.props.title, subtitle: text(dialog.props.subtitle), content: text(dialog.props.children), keys })
  }
} finally { Date.now = actualNow }
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ rows, durations: input.durations.map((ms: number) => formatDuration(Math.max(0, ms))), blocked: capabilities.blocked }), error => error ? reject(error) : resolve()))
