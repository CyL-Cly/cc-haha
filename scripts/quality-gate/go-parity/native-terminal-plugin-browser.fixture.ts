import fs from 'node:fs'
import { createRequire } from 'node:module'
import { assertDisposableFixtureHome } from './fixture-guard'

const input = await Bun.stdin.json()
assertDisposableFixtureHome(input.home, 'Original plugin browser URL/OS opener')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_TERMINAL_PLUGIN_BROWSER_SOURCE !== '1') {
  throw new Error('Missing plugin browser Source guard')
}
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
const relative = '../../../src/utils/browser.ts'
const source = ts.createSourceFile(relative, fs.readFileSync(new URL(relative, import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TS)
const names = ['validateUrl', 'openBrowser']
const nodes = source.statements.filter((node: any) => ts.isFunctionDeclaration(node) && names.includes(node.name?.text))
if (nodes.length !== names.length) throw new Error('Original browser declarations changed')
const code = ts.transpileModule(nodes.map((node: any) => node.getText(source).replace(/^export /, '')).join('\n'), { compilerOptions: { target: ts.ScriptTarget.ESNext } }).outputText
const execFileNoThrow = async (command: string, args: string[]) => {
  const child = Bun.spawn([command, ...args], { cwd: input.home, env: input.environment, stdin: 'ignore', stdout: 'ignore', stderr: 'ignore' })
  return { code: await child.exited }
}
const original = new Function('process', 'execFileNoThrow', code + '; return openBrowser')({ platform: input.platform, env: input.environment }, execFileNoThrow)
const values = []
for (const address of input.addresses) values.push(await original(address))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify(values), error => error ? reject(error) : resolve()))
