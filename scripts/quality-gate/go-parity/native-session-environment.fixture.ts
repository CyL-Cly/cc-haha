import { parse } from 'acorn'
import { mkdir, readdir, readFile, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Original session environment')
if (process.env.CC_HAHA_SESSION_ENV_FIXTURE !== '1') throw new Error('Unowned session environment fixture')
const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL('../../../src/utils/sessionEnvironment.ts', import.meta.url)).text())
const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
const functions = ast.body.map((n: any) => n.type === 'ExportNamedDeclaration' ? n.declaration : n).filter((n: any) => n?.type === 'FunctionDeclaration')
const names = ['getSessionEnvDirPath', 'getHookEnvFilePath', 'clearCwdEnvFiles', 'invalidateSessionEnvCache', 'getSessionEnvironmentScript', 'sortHookEnvFiles']
const bodies = names.map(name => { const node = functions.find((n: any) => n.id.name === name); if (!node) throw new Error('Missing session environment Source ' + name); return source.slice(node.start, node.end) }).join('\n')
const constants = ast.body.filter((n: any) => n.type === 'VariableDeclaration' && n.declarations.some((d: any) => ['HOOK_ENV_PRIORITY', 'HOOK_ENV_REGEX'].includes(d.id.name))).map((n: any) => source.slice(n.start, n.end)).join('\n')
const rows = []
for (const row of input.rows) {
  const config = join(input.homeDir, row.name)
  const parent = join(config, 'parent.sh')
  await mkdir(config, { recursive: true })
  await writeFile(parent, Buffer.from(row.parent, 'base64'))
  let sid = 'first-session'
  const original = new Function('mkdir', 'readdir', 'readFile', 'writeFile', 'join', 'getSessionId', 'logForDebugging', 'getClaudeConfigHomeDir', 'errorMessage', 'getErrnoCode', 'getPlatform', 'process', 'let sessionEnvScript;\n' + constants + '\n' + bodies + '\nreturn {getSessionEnvDirPath,getHookEnvFilePath,clearCwdEnvFiles,invalidateSessionEnvCache,getSessionEnvironmentScript,sortHookEnvFiles}') (mkdir, readdir, readFile, writeFile, join, () => sid, () => {}, () => config, (e: any) => e.message, (e: any) => e.code, () => row.platform, { env: { CLAUDE_ENV_FILE: parent } })
  const directory = await original.getSessionEnvDirPath()
  for (const [name, bytes] of Object.entries(row.files)) await writeFile(join(directory, name), Buffer.from(bytes as string, 'base64'))
  const paths = []
  for (const [event, index] of row.paths) paths.push(await original.getHookEnvFilePath(event, index))
  const first = await original.getSessionEnvironmentScript()
  await writeFile(join(directory, 'sessionstart-hook-0.sh'), 'export NEW="after-first-read"\n')
  const cached = await original.getSessionEnvironmentScript()
  original.invalidateSessionEnvCache()
  const invalidated = await original.getSessionEnvironmentScript()
  await original.clearCwdEnvFiles()
  original.invalidateSessionEnvCache()
  const cleared = await original.getSessionEnvironmentScript()
  sid = 'second-session'
  const switchedCached = await original.getSessionEnvironmentScript()
  original.invalidateSessionEnvCache()
  const switched = await original.getSessionEnvironmentScript()
  rows.push({ paths: paths.map((p: string) => p.slice(config.length)), first, cached, invalidated, cleared, switchedCached, switched, sorted: row.sort.slice().sort(original.sortHookEnvFiles) })
}
process.stdout.write(JSON.stringify({ rows }))
