import fs from 'node:fs'
import { createRequire } from 'node:module'
import { assertDisposableFixtureHome } from './fixture-guard'

const input = await Bun.stdin.json()
assertDisposableFixtureHome(input.home, 'Original post-enable plugin options/channel fields')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_TERMINAL_PLUGIN_OPTIONS_FLOW_SOURCE !== '1') throw new Error('Missing plugin options Source guard')
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
function compile(relative: string, names: string[], deps: any, returns: string) {
  const source = ts.createSourceFile(relative, fs.readFileSync(new URL('../../../src/' + relative, import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TS)
  const nodes = source.statements.filter((node: any) => ts.isFunctionDeclaration(node) && names.includes(node.name?.text))
  if (nodes.length !== names.length) throw new Error('Original declarations changed: ' + relative)
  const code = ts.transpileModule(nodes.map((node: any) => node.getText(source).replace(/^export /, '')).join('\n'), { compilerOptions: { target: ts.ScriptTarget.ESNext } }).outputText
  return new Function(...Object.keys(deps), code + '; return ' + returns)(...Object.values(deps))
}
const validateUserConfig = compile('utils/plugins/mcpbHandler.ts', ['validateUserConfig'], {}, 'validateUserConfig')
const observations = input.rows.map((row: any) => {
  const top = compile('utils/plugins/pluginOptionsStorage.ts', ['getPluginStorageId', 'getUnconfiguredOptions'], { validateUserConfig, loadPluginOptions: (id: string) => {
    if (id !== row.plugin.source) throw new Error('Wrong top-level option identity')
    return row.saved
  } }, 'getUnconfiguredOptions')(row.plugin)
  const channels = compile('utils/plugins/mcpPluginIntegration.ts', ['getUnconfiguredChannels'], { validateUserConfig, loadMcpServerUserConfig: (id: string, server: string) => {
    if (id !== row.plugin.repository) throw new Error('Wrong channel option identity')
    return row.channels[server]
  } }, 'getUnconfiguredChannels')(row.plugin)
  return { top, channels: channels.map((channel: any) => ({ server: channel.server, schema: channel.configSchema })) }
})
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify(observations), error => error ? reject(error) : resolve()))
