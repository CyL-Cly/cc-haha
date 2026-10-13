import { assertDisposableFixtureHome } from './fixture-guard'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Windows native process argv')
const path = await import('node:path')
const escapeSource = await Bun.file(new URL('../../../node_modules/cross-spawn/lib/util/escape.js', import.meta.url)).text()
const escapeModule: any = { exports: {} }
new Function('module', escapeSource)(escapeModule)
const parseSource = await Bun.file(new URL('../../../node_modules/cross-spawn/lib/parse.js', import.meta.url)).text()
const outputs = input.cases.map((row: any) => {
  const module: any = { exports: {} }
  const deps: Record<string, any> = {
    path: path.win32,
    './util/resolveCommand': () => row.commandFile,
    './util/readShebang': () => null,
    './util/escape': escapeModule.exports,
  }
  new Function('require', 'module', 'process', parseSource)((name: string) => {
    if (!(name in deps)) throw new Error(`Unexpected actual cross-spawn dependency ${name}`)
    return deps[name]
  }, module, { platform: 'win32', env: row.environment })
  const result = module.exports(row.command, row.arguments, {})
  return { Command: result.command, Arguments: result.args, Verbatim: result.options.windowsVerbatimArguments ?? false }
})
process.stdout.write(JSON.stringify(outputs))
