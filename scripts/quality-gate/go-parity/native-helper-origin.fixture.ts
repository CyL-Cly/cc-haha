import { parse } from 'acorn'
import { assertDisposableFixtureHome } from './fixture-guard'

assertDisposableFixtureHome(process.env.HOME, 'Native helper provenance')
if (process.env.CC_HAHA_HELPER_ORIGIN_FIXTURE !== '1') throw new Error('Unowned helper provenance fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL('../../../src/utils/auth.ts', import.meta.url)).text())
const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
const functions = ast.body.map((node: any) => node.type === 'ExportNamedDeclaration' ? node.declaration : node)
const names = ['isApiKeyHelperFromProjectOrLocalSettings', 'isAwsAuthRefreshFromProjectSettings', 'isAwsCredentialExportFromProjectSettings', 'isGcpAuthRefreshFromProjectSettings']
const code = names.map(name => {
  const node = functions.find((node: any) => node?.type === 'FunctionDeclaration' && node.id.name === name)
  if (!node) throw new Error('Original helper provenance missing: ' + name)
  return source.slice(node.start, node.end)
}).join('\n')
const build = new Function('getConfiguredApiKeyHelper', 'getConfiguredAwsAuthRefresh', 'getConfiguredAwsCredentialExport', 'getConfiguredGcpAuthRefresh', 'getSettingsForSource', code + ';return [' + names.join(',') + ']')
const result = input.rows.map((row: any) => build(...['apiKeyHelper', 'awsAuthRefresh', 'awsCredentialExport', 'gcpAuthRefresh'].map(name => () => row.settings[name]), (name: string) => row[name]).map((fn: any) => fn()))
process.stdout.write(JSON.stringify(result))
