import { parse } from 'acorn'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
assertDisposableFixtureHome(input.homeDir, 'Terminal pasted media Source')
if (process.env.CC_HAHA_TERMINAL_PASTED_MEDIA_FIXTURE !== '1') throw new Error('Unowned terminal media fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
state.setIsInteractive(true)
state.setCwdState(input.homeDir)
state.setOriginalCwd(input.homeDir)
state.setProjectRoot(input.homeDir)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const { processTextPrompt } = await import('../../../src/utils/processUserInput/processTextPrompt')
const { createUserMessage } = await import('../../../src/utils/messages')
const { maybeResizeAndDownsampleImageBlock, createImageMetadataText } = await import('../../../src/utils/imageResizer')
const { isValidImagePaste } = await import('../../../src/types/textInputTypes')
const { storeImages } = await import('../../../src/utils/imageStore')
const text = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL('../../../src/utils/processUserInput/processUserInput.ts', import.meta.url)).text())
const ast: any = parse(text, { ecmaVersion: 'latest', sourceType: 'module' })
const nodes = ast.body.map((node: any) => node.type === 'ExportNamedDeclaration' ? node.declaration : node)
let extracted = ['processUserInputBase', 'addImageMetadataMessage'].map(name => {
  const node = nodes.find((node: any) => node.type === 'FunctionDeclaration' && node.id.name === name)
  if (!node) throw new Error('Missing actual input function ' + name)
  return text.slice(node.start, node.end)
}).join('\n')
// Intercept only the downstream slash-command sink. The extracted Source base
// still performs the actual paste/cache/normalization/metadata transitions;
// application regression separately invokes the real Markdown skill/Harness.
const imports: Array<{ start: number; end: number }> = []
const walk = (node: any) => {
  if (!node || typeof node !== 'object') return
  if (node.type === 'ImportExpression' && node.source?.value === './processSlashCommand.js') imports.push(node)
  for (const [key, value] of Object.entries(node)) {
    if (key === 'start' || key === 'end') continue
    if (Array.isArray(value)) value.forEach(walk)
    else if (value && typeof value === 'object') walk(value)
  }
}
walk(parse(extracted, { ecmaVersion: 'latest', sourceType: 'script' }))
// Pinned Bun removes the frozen-disabled ULTRAPLAN branch during transpilation.
if (imports.length !== 1) throw new Error('Actual Source slash import boundaries changed')
for (const node of imports.sort((a,b) => b.start-a.start)) extracted = extracted.slice(0,node.start) + 'loadSlash()' + extracted.slice(node.end)
const loadSlash = async () => ({ processSlashCommand: async (text: string, preceding: any[], images: any[], _attachments: any[], _context: any, _setJSX: any, uuid: string) => ({ messages: [createUserMessage({ content: [...preceding, ...images, { type: 'text', text }], uuid })], shouldQuery: true, allowedTools: [] }) })
const run = new Function('queryCheckpoint', 'maybeResizeAndDownsampleImageBlock', 'createImageMetadataText', 'feature', 'processTextPrompt', 'createUserMessage', 'isValidImagePaste', 'storeImages', 'logEvent', 'loadSlash', extracted + '\nreturn processUserInputBase')(
  () => {}, maybeResizeAndDownsampleImageBlock, createImageMetadataText, () => false, processTextPrompt, createUserMessage, isValidImagePaste, storeImages, () => {},
  loadSlash,
)
const rows = []
for (const row of input.rows) {
  const value = await run(row.text, 'prompt', () => {}, { options: { commands: [], isNonInteractiveSession: false } }, row.pastes, undefined, [], 'fixed-input-uuid', false, 'terminal', undefined, 'default', !row.slash, false, false, true)
  rows.push({ content: value.messages[0].message.content, ids: value.messages[0].imagePasteIds ?? [], metadata: (value.messages[1]?.message.content ?? []).map((block: any) => block.text) })
}
if (capabilities.blocked.length) throw new Error('Blocked terminal media Source ' + capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({ rows, sessionID: state.getSessionId() }))
