import { assertDisposableFixtureHome } from './fixture-guard'
import { parse } from 'acorn'
import * as xml from '../../../src/constants/xml'
import { REJECT_MESSAGE } from '../../../src/constants/messages'
import { stripIdeContextTags } from '../../../src/utils/displayTags'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'message selector')
if (process.env.CC_HAHA_NATIVE_MESSAGE_SELECTOR_FIXTURE !== '1') throw new Error('selector fixture guard missing')

// Run the unchanged functions from frozen MAIN with their lexical inputs.
// This avoids mounting React, provider clients or any application state.
function bindings(source: string, loader: 'ts' | 'tsx', names: string[]): string {
  const code = new Bun.Transpiler({ loader }).transformSync(source)
  const ast: any = parse(code, { ecmaVersion: 'latest', sourceType: 'module' })
  const found = new Map<string, string>()
  for (const node of ast.body) {
    const declaration = node.type === 'ExportNamedDeclaration' ? node.declaration : node
    if (declaration?.type === 'FunctionDeclaration' && names.includes(declaration.id.name)) found.set(declaration.id.name, code.slice(declaration.start, declaration.end))
    if (declaration?.type === 'VariableDeclaration') {
      for (const item of declaration.declarations) if (names.includes(item.id?.name)) found.set(item.id.name, `const ${code.slice(item.start, item.end)};`)
    }
  }
  for (const name of names) if (!found.has(name)) throw new Error(`Actual Source binding missing: ${name}`)
  return names.map(name => found.get(name)).join('\n')
}
const messages = await Bun.file(new URL('../../../src/utils/messages.ts', import.meta.url)).text()
const selector = await Bun.file(new URL('../../../src/components/MessageSelector.tsx', import.meta.url)).text()
const source = bindings(messages, 'ts', ['INTERRUPT_MESSAGE', 'INTERRUPT_MESSAGE_FOR_TOOL_USE', 'CANCEL_MESSAGE', 'NO_RESPONSE_REQUESTED', 'SYNTHETIC_MESSAGES', 'isSyntheticMessage', 'extractTextContent', 'getContentText', 'getUserMessageText', 'extractTag', 'textForResubmit']) + '\n' + bindings(selector, 'tsx', ['isTextBlock', 'selectableUserMessagesFilter'])
const names = Object.keys(xml)
const run = new Function(...names, 'REJECT_MESSAGE', 'stripIdeContextTags', 'escapeRegExp', `${source}\nreturn message => ({selectable: selectableUserMessagesFilter(message), resubmit: textForResubmit(message)})`)(...names.map(name => (xml as any)[name]), REJECT_MESSAGE, stripIdeContextTags, (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'))
process.stdout.write(JSON.stringify({ rows: input.rows.map(run) }))
