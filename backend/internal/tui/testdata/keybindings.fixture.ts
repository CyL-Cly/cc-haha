import fs from 'node:fs'
import { join } from 'node:path'
import { mock } from 'bun:test'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_KEYBINDING_FIXTURE !== '1'
  || Object.keys(input).some(key => !['home', 'platform', 'keys', 'chords', 'blocks', 'events', 'slashes'].includes(key))) throw new Error('Keybinding comparison requires original private inputs and Bun 1.3.14')
const home = assertDisposableFixtureHome(input.home, 'TUI keybindings')
fs.mkdirSync(join(home, '.claude'), { recursive: true })
Object.assign(globalThis, { MACRO: { VERSION: 'fixture', PACKAGE_URL: 'fixture', NATIVE_PACKAGE_URL: 'fixture', BUILD_TIME: 'fixture', FEEDBACK_CHANNEL: 'local', VERSION_CHANGELOG: '', ISSUES_EXPLAINER: '' } })
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
// Platform is an original host input. Only the platform adapter is replaced;
// parsing, matching, override and default-binding decisions execute Source.
mock.module(new URL('../../../../src/utils/platform.ts', import.meta.url).pathname, () => ({ getPlatform: () => input.platform }))
const parser = await import('../../../../src/keybindings/parser')
const resolver = await import('../../../../src/keybindings/resolver')
const { DEFAULT_BINDINGS } = await import('../../../../src/keybindings/defaultBindings')
const { generateKeybindingsTemplate } = await import('../../../../src/keybindings/template')
const { parseSlashCommand } = await import('../../../../src/utils/slashCommandParsing')
const bindings = parser.parseBindings(input.blocks)
function incoming(current: any) {
  if (current === null) return { input: '', key: {} }
  const key: any = { ctrl: current.ctrl, shift: current.shift, meta: current.alt || current.meta, super: current.super }
  const named: Record<string, string> = { escape: 'escape', enter: 'return', tab: 'tab', backspace: 'backspace', delete: 'delete', up: 'upArrow', down: 'downArrow', left: 'leftArrow', right: 'rightArrow', pageup: 'pageUp', pagedown: 'pageDown', wheelup: 'wheelUp', wheeldown: 'wheelDown', home: 'home', end: 'end' }
  if (named[current.key]) key[named[current.key]] = true
  return { input: current.key, key }
}
const events = input.events.map((event: any) => {
  const { input: text, key } = incoming(event.current)
  return {
    single: resolver.resolveKey(text, key, event.contexts, bindings),
    chord: resolver.resolveKeyWithChordState(text, key, event.contexts, bindings, event.pending),
  }
})
process.stdout.write(JSON.stringify({
  keys: input.keys.map((key: string) => {
    const parsed = parser.parseKeystroke(key)
    return { parsed, canonical: parser.keystrokeToString(parsed), display: parser.keystrokeToDisplayString(parsed, input.platform) }
  }),
  chords: input.chords.map((chord: string) => { const parsed = parser.parseChord(chord); return { parsed, canonical: parser.chordToString(parsed) } }),
  bindings, defaults: parser.parseBindings(DEFAULT_BINDINGS), template: generateKeybindingsTemplate(), events,
  displays: bindings.map((binding: any) => resolver.getBindingDisplayText(binding.action, binding.context, bindings) ?? null),
  slashes: input.slashes.map(parseSlashCommand), capabilities,
}))
