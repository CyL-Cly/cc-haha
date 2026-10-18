import fs from 'node:fs'
import { createRequire } from 'node:module'
import { assertDisposableFixtureHome } from './fixture-guard'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Original stickers call and browser OS consumer')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_STICKERS_SOURCE !== '1' || !input.environment.BROWSER.startsWith(home + '/')) throw new Error('Private stickers Source required')
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
function functions(relative: string, names: string[]) {
  const text = fs.readFileSync(new URL(relative, import.meta.url), 'utf8')
  const source = ts.createSourceFile(relative, text, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS)
  const nodes = source.statements.filter((node: any) => ts.isFunctionDeclaration(node) && names.includes(node.name?.text))
  if (nodes.length !== names.length) throw new Error('Original stickers declarations changed')
  return ts.transpileModule(nodes.map((node: any) => node.getText(source).replace(/^export /, '')).join('\n'), { compilerOptions: { target: ts.ScriptTarget.ESNext } }).outputText
}
const execFileNoThrow = async (command: string, args: string[]) => {
  if (command !== input.environment.BROWSER || args.length !== 1 || args[0] !== 'https://www.stickermule.com/claudecode') throw new Error('Unowned browser primitive')
  const child = Bun.spawn([command, ...args], { cwd: home, env: input.environment, stdin: 'ignore', stdout: 'ignore', stderr: 'ignore' })
  return { code: await child.exited }
}
const openBrowser = new Function('process', 'execFileNoThrow', functions('../../../src/utils/browser.ts', ['validateUrl', 'openBrowser']) + '; return openBrowser')({ platform: 'darwin', env: input.environment }, execFileNoThrow)
const call = new Function('openBrowser', functions('../../../src/commands/stickers/stickers.ts', ['call']) + '; return call')(openBrowser)
const result = await call()
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify(result), error => error ? reject(error) : resolve()))
