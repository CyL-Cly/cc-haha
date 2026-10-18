import fs from 'node:fs'
import { createRequire } from 'node:module'
import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Original REPL stream callback and spinner counters')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_SPINNER_EVENTS_SOURCE !== '1') throw new Error('Pinned original spinner stream caller required')
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
const source = ts.createSourceFile('REPL.tsx', fs.readFileSync(new URL('../../../src/screens/REPL.tsx', import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
let lengthCallback: any
function visit(node: any) {
  if (ts.isCallExpression(node) && node.expression.getText(source) === 'handleMessageFromStream') lengthCallback = node.arguments[2]
  ts.forEachChild(node, visit)
}
visit(source)
if (!lengthCallback) throw new Error('Original REPL onUpdateLength consumer missing')
const code = ts.transpileModule(`const value=(${lengthCallback.getText(source)})`, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText
process.env.USER_TYPE = 'external'
process.env.ANTHROPIC_API_KEY = 'spinner-stream-private-api-key'
fs.mkdirSync(join(home, '.claude'), { recursive: true })
fs.writeFileSync(join(home, '.claude/settings.json'), '{}')
const absent = absentManagedSettingsFixturePaths()
for (let directory = dirname(home);; directory = dirname(directory)) {
  const path = join(directory, '.git')
  if (!fs.existsSync(path)) absent.push(path)
  if (dirname(directory) === directory) break
}
const caps = await installClientFixtureCapabilities(home, { absentPaths: absent })
await import('../../../preload')
const { handleMessageFromStream } = await import('../../../src/utils/messages')
const results: any[] = []
for (const messages of input.rows) {
  let mode = 'responding', length = 0, toolUses: any[] = []
  const lengthConsumer = new Function('setResponseLength', code + ';return value')((update: any) => length = update(length))
  const frames: any[] = []
  for (const message of messages) {
    const beforeLength = length, beforeMode = mode
    handleMessageFromStream(message, () => {}, lengthConsumer, value => mode = value, update => toolUses = update(toolUses))
    frames.push({ mode, length, added: length - beforeLength, modeChanged: mode !== beforeMode, toolUses })
  }
  results.push(frames)
}
if (caps.blocked.length || caps.secureReads.length) throw new Error('Original spinner event consumer escaped private fixture ' + JSON.stringify(caps))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ results, caps }), error => error ? reject(error) : resolve()))
process.exit(0)
