import { createRequire } from 'node:module'
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.home, 'Public beta filter Source')
if (process.env.CC_HAHA_CLI_BETA_FIXTURE !== '1') throw new Error('Unowned beta fixture')
const sourcePath = resolve(import.meta.dir, '../../../src/utils/betas.ts')
const source = readFileSync(sourcePath, 'utf8')
const file = ts.createSourceFile(sourcePath, source, ts.ScriptTarget.Latest, true)
const node = file.statements.find(node => ts.isFunctionDeclaration(node) && node.name?.text === 'filterAllowedSdkBetas')!
const partition = file.statements.find(node => ts.isFunctionDeclaration(node) && node.name?.text === 'partitionBetasByAllowlist')!
if (!node || !partition) throw new Error('Missing original beta functions')
const compiler = new Bun.Transpiler({loader:'ts'})
const body = compiler.transformSync(partition.getText(file) + '\n' + node.getText(file).replace('export ', ''))
const mergeNode = file.statements.find((node: any) => ts.isFunctionDeclaration(node) && node.name?.text === "getMergedBetas")
if (input.merged) {
  if (!mergeNode) throw new Error("Missing Source merge producer")
  const mergeBody = compiler.transformSync(mergeNode.getText(file).replace("export ", ""))
  const env = await import("../../../src/utils/envUtils")
  const values = input.merged.map((row: any) => new Function("getAPIProvider", "isFirstPartyAnthropicBaseUrl", "isEnvTruthy", "has1mContext", "CONTEXT_1M_BETA_HEADER", "getEnvironmentBetas", "getSdkBetas", "getModelBetas", "process", mergeBody + "\nreturn getMergedBetas(\"model\")")(() => row.provider, () => !row.custom, env.isEnvTruthy, () => row.has1m, "context-1m-2025-08-07", () => row.environment, () => row.sdk, () => row.base, {env:row.env??{}}))
  process.stdout.write(JSON.stringify(values))
  process.exit(0)
}
const rows = input.rows.map((row: any) => {
  const warnings: string[] = []
  // The original pure filter is exercised with the caller's identity as its
  // dependency. Native authentication itself is tested through MainPrint.
  return new Function('isClaudeAISubscriber','console','ALLOWED_SDK_BETAS','values','warnings',body+'\nreturn {allowed:filterAllowedSdkBetas(values) ?? null,warnings}')(() => row.subscriber,{warn:(text:string)=>warnings.push(text)},['context-1m-2025-08-07'],row.values,warnings)
})
process.stdout.write(JSON.stringify(rows))
