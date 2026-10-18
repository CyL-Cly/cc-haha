import fs from 'node:fs'
import fsp from 'node:fs/promises'
import { createRequire } from 'node:module'
import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Original keybindings command and exclusive template creation')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_KEYBINDINGS_COMMAND_SOURCE !== '1') throw new Error('Pinned keybindings command Source required')
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
const caps = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
;(globalThis as any).MACRO = { VERSION: '999.0.0-keybindings-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
const { generateKeybindingsTemplate } = await import('../../../src/keybindings/template')
function original(path: string, names: string[], dependencies: Record<string, unknown>, returned: string) {
  const source = ts.createSourceFile(path, fs.readFileSync(new URL(path, import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TS)
  const nodes = source.statements.filter((node: any) => ts.isFunctionDeclaration(node) && names.includes(node.name?.text))
  if (nodes.length !== names.length) throw new Error('Original keybindings function identity changed')
  const js = ts.transpileModule(nodes.map((node: any) => node.getText(source).replace(/^export /, '')).join('\n'), { compilerOptions: { target: ts.ScriptTarget.ESNext, module: ts.ModuleKind.ESNext } }).outputText
  return new Function(...Object.keys(dependencies), js + '\nreturn ' + returned)(...Object.values(dependencies))
}
const path = join(home, '.claude', 'keybindings.json')
await fsp.mkdir(dirname(path), { recursive: true })
if (input.existing !== undefined) await fsp.writeFile(path, input.existing)
const gates: unknown[] = [], editors: unknown[] = []
const enabled = original('../../../src/keybindings/loadUserBindings.ts', ['isKeybindingCustomizationEnabled'], { getFeatureValue_CACHED_MAY_BE_STALE: (name: string, fallback: boolean) => { gates.push([name, fallback]); return input.enabled } }, 'isKeybindingCustomizationEnabled')
const call = original('../../../src/commands/keybindings/keybindings.ts', ['call'], {
  mkdir: fsp.mkdir, writeFile: fsp.writeFile, dirname, getKeybindingsPath: () => path,
  isKeybindingCustomizationEnabled: enabled, generateKeybindingsTemplate,
  getErrnoCode: (error: NodeJS.ErrnoException) => error.code,
  editFileInEditor: (requested: string) => {
    if (requested !== path) throw new Error('Unowned editor path')
    editors.push({ path: requested, before: fs.readFileSync(requested, 'utf8') })
    if (input.edited !== undefined) fs.writeFileSync(requested, input.edited)
    return { content: null, ...(input.editorError ? { error: input.editorError } : {}) }
  },
}, 'call')
const result = await call()
if (caps.blocked.length || caps.secureReads.length) throw new Error('Keybindings Source escaped private fixture')
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ result, editors, gates, template: generateKeybindingsTemplate(), content: fs.existsSync(path) ? fs.readFileSync(path, 'utf8') : null, caps }), error => error ? reject(error) : resolve()))
