import { parse } from 'acorn'
import { isIP } from 'node:net'
import { assertDisposableFixtureHome } from './fixture-guard'
assertDisposableFixtureHome(process.env.HOME, 'Image CDN public address policy')
if (process.env.CC_HAHA_IMAGE_DOWNLOAD_ADDRESS_FIXTURE !== '1') throw new Error('Unowned image CDN fixture')
async function functions(path: string, names: string[]) {
  const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL(path, import.meta.url)).text())
  const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
  const declared = ast.body.map((node: any) => node.type === 'ExportNamedDeclaration' ? node.declaration : node).filter((node: any) => node?.type === 'FunctionDeclaration')
  return names.map(name => {
    const node = declared.find((node: any) => node.id?.name === name)
    if (!node) throw new Error('Source image address declaration missing: ' + name)
    return source.slice(node.start, node.end)
  }).join('\n')
}
const blocked = new Function('isIP', await functions('../../../src/utils/hooks/ssrfGuard.ts', ['isBlockedAddress', 'isBlockedV4', 'isBlockedV6', 'expandIPv6Groups', 'extractMappedIPv4']) + '\nreturn isBlockedAddress')(isIP)
const actual = new Function('isIP', 'isBlockedAddress', await functions('../../../src/tools/ImageGenTool/imageDownload.ts', ['assertPublicAddress']) + '\nreturn assertPublicAddress')(isIP, blocked)
const rows = await Bun.stdin.json()
process.stdout.write(JSON.stringify(rows.map((address: string) => { try { actual(address); return '' } catch (error) { return (error as Error).message } })))
