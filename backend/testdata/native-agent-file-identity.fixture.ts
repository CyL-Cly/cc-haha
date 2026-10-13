import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import { lstat } from 'node:fs/promises'
import { parse } from 'acorn'
const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Agent file identity')
if (process.env.CC_HAHA_AGENT_IDENTITY_FIXTURE !== '1') throw new Error('Unowned Agent identity fixture')
const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL('../../src/utils/markdownConfigLoader.ts', import.meta.url)).text())
const ast = parse(source, { ecmaVersion: 'latest', sourceType: 'module' }) as any
const node = ast.body.find((top: any) => top.type === 'FunctionDeclaration' && top.id.name === 'getFileIdentity')
if (!node) throw new Error('Actual Source getFileIdentity unavailable')
let active: any
const stat = async (path: string, options: any) => {
  if (options.bigint !== true) throw new Error('Source lost bigint identity')
  if (active.physical) {
    if (!path.startsWith(home + '/')) throw new Error('Unowned physical identity')
    return lstat(path, options)
  }
  if (active.error) throw new Error('Owned native identity failure')
  return { dev: BigInt(active.device), ino: BigInt(active.inode) }
}
const getIdentity = new Function('lstat', `return (${source.slice(node.start, node.end)})`)(stat)
const identities = []
for (active of input.rows) identities.push(await getIdentity(active.path ?? 'owned'))
process.stdout.write(JSON.stringify({ identities }) + '\n')
