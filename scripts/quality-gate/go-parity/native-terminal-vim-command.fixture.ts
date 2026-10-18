import fs from 'node:fs'
import { createRequire } from 'node:module'
import { assertDisposableFixtureHome } from './fixture-guard'

const input = await Bun.stdin.json()
assertDisposableFixtureHome(input.home, 'Actual Vim local command')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_VIM_COMMAND_SOURCE !== '1') throw new Error('Missing original Vim command guard')
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
function original(path: string, deps: Record<string, unknown>, returned: string) {
  const source = ts.createSourceFile(path, fs.readFileSync(new URL(path, import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TS)
  const body = source.statements.filter((node: any) => !ts.isImportDeclaration(node) && !ts.isExportAssignment(node)).map((node: any) => node.getText(source).replace(/^export /, '')).join('\n')
  const js = ts.transpileModule(body, { compilerOptions: { target: ts.ScriptTarget.ESNext, module: ts.ModuleKind.ESNext } }).outputText
  return new Function(...Object.keys(deps), js + '\nreturn ' + returned)(...Object.values(deps))
}
const rows = []
for (const item of input.cases) {
  let global = structuredClone(item.global)
  const saves: unknown[] = [], events: unknown[] = []
  const call = original('../../../src/commands/vim/vim.ts', {
    getGlobalConfig: () => global,
    saveGlobalConfig: (update: any) => { global = update(global); saves.push(structuredClone(global)) },
    logEvent: (name: string, metadata: unknown) => events.push({ name, metadata }),
  }, 'call')
  const result = await call()
  rows.push({ result, global, saves, events })
}
const definition = original('../../../src/commands/vim/index.ts', {}, 'command')
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ definition, rows }) + '\n', error => error ? reject(error) : resolve()))
