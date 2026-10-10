import { parse } from 'acorn'
import { EventEmitter } from 'node:events'
import { assertDisposableFixtureHome } from './fixture-guard'

assertDisposableFixtureHome(process.env.HOME, 'Native AWS credential helpers')
if (process.env.CC_HAHA_AWS_HELPERS_FIXTURE !== '1') throw new Error('Unowned AWS helper fixture')
const input = await Bun.stdin.json()
const transpiler = new Bun.Transpiler({ loader: 'ts' })
const source = transpiler.transformSync(await Bun.file(new URL('../../../src/utils/auth.ts', import.meta.url)).text())
const aws = transpiler.transformSync(await Bun.file(new URL('../../../src/utils/aws.ts', import.meta.url)).text())
const declarations = (text: string) => (parse(text, { ecmaVersion: 'latest', sourceType: 'module' }) as any).body.map((node: any) => node.type === 'ExportNamedDeclaration' ? node.declaration : node)
const functions = declarations(source)
const names = ['runAwsAuthRefresh', 'refreshAwsAuth', 'getAwsCredsFromCredentialExport']
const code = names.map(name => {
  const declaration = functions.find((node: any) => node?.type === 'FunctionDeclaration' && node.id.name === name)
  if (!declaration) throw new Error('Original AWS helper missing ' + name)
  return source.slice(declaration.start, declaration.end)
}).join('\n')
const combined = functions.find((node: any) => node?.type === 'VariableDeclaration' && node.declarations.some((item: any) => item.id.name === 'refreshAndGetAwsCredentials'))
const valid = declarations(aws).find((node: any) => node?.type === 'FunctionDeclaration' && node.id.name === 'isValidAwsStsOutput')
if (!combined || !valid) throw new Error('Original combined or validator missing')
const build = new Function('getConfiguredAwsAuthRefresh', 'getConfiguredAwsCredentialExport', 'isAwsAuthRefreshFromProjectSettings', 'isAwsCredentialExportFromProjectSettings', 'checkHasTrustDialogAccepted', 'getIsNonInteractiveSession', 'checkStsCallerIdentity', 'exec', 'execa', 'clearAwsIniCache', 'console', `
const logForDebugging=()=>{},logAntError=()=>{},logEvent=()=>{},chalk={red:x=>x},MACRO={FEEDBACK_CHANNEL:'fixture'};
const AWS_AUTH_REFRESH_TIMEOUT_MS=180000,DEFAULT_AWS_STS_TTL=3600000,jsonParse=JSON.parse,memoizeWithTTLAsync=fn=>fn;
const AwsAuthStatusManager={getInstance:()=>({startAuthentication(){},addOutput(){},setError(){},endAuthentication(){}})};
${aws.slice(valid.start, valid.end)}\n${code}\n${source.slice(combined.start, combined.end)}
return refreshAndGetAwsCredentials;`)
const rows = []
for (const row of input.rows) {
  const calls: string[] = []
  let probe = 0
  const run = build(() => row.refresh || '', () => row.export || '', () => !!row.refreshProject, () => !!row.exportProject, () => !!row.trusted, () => !!row.nonInteractive,
    async () => { calls.push('probe'); if (!row.probes?.[probe++]) throw new Error('private invalid identity') },
    (command: string, options: any) => {
      calls.push(`refresh:${command}:${options.timeout}`)
      const proc: any = new EventEmitter()
      proc.stdout = new EventEmitter(); proc.stderr = new EventEmitter()
      queueMicrotask(() => proc.emit('close', row.refreshExit ?? 0, null))
      return proc
    },
    async (command: string) => { calls.push(`export:${command}`); return { exitCode: row.exportExit ?? 0, stdout: row.stdout ?? '' } },
    async () => { calls.push('clear'); if (row.clearFails) return },
    { error() {} })
  rows.push({ value: await run(), calls })
}
console.log(JSON.stringify({ rows }))
