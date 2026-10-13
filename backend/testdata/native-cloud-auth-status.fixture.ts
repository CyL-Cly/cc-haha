import { parse } from 'acorn'
import { EventEmitter } from 'node:events'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
assertDisposableFixtureHome(process.env.HOME, 'Actual cloud authentication status')
if (process.env.CC_HAHA_CLOUD_STATUS_FIXTURE !== '1') throw new Error('Unowned cloud status fixture')
const input = await Bun.stdin.json()
const { AwsAuthStatusManager } = await import('../../src/utils/awsAuthStatusManager')
const manager = AwsAuthStatusManager.getInstance()
const events: unknown[] = []
const dispose = manager.subscribe(value => events.push(value))
if (input.kind === 'refresh') {
  const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL('../../src/utils/auth.ts', import.meta.url)).text())
  const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
  const name = input.provider === 'gcp' ? 'refreshGcpAuth' : 'refreshAwsAuth'
  const declaration = ast.body.map((node: any) => node.type === 'ExportNamedDeclaration' ? node.declaration : node).find((node: any) => node?.type === 'FunctionDeclaration' && node.id.name === name)
  if (!declaration) throw new Error('Original cloud refresh function missing')
  const exec = () => {
    const child: any = new EventEmitter()
    child.stdout = new EventEmitter()
    child.stderr = new EventEmitter()
    queueMicrotask(() => {
      if (input.stdout !== undefined) child.stdout.emit('data', Buffer.from(input.stdout))
      if (input.stderr !== undefined) child.stderr.emit('data', Buffer.from(input.stderr))
      child.emit('close', input.code, input.signal ?? null)
    })
    return child
  }
  const run = new Function('AwsAuthStatusManager', 'exec', `const logForDebugging=()=>{},chalk={red:x=>x},console={error(){}},AWS_AUTH_REFRESH_TIMEOUT_MS=180000,GCP_AUTH_REFRESH_TIMEOUT_MS=180000;${source.slice(declaration.start,declaration.end)};return ${name}`)(AwsAuthStatusManager, exec)
  const success = await run('fixture external subprocess')
  process.stdout.write(JSON.stringify({ success, events, status: manager.getStatus() }))
} else {
  const visits: string[] = []
  let removeSecond: (() => void) | undefined
  let third: (() => void) | undefined
  const first = manager.subscribe(() => {
    visits.push('first')
    removeSecond?.()
    third ??= manager.subscribe(() => visits.push('third'))
  })
  removeSecond = manager.subscribe(() => visits.push('second'))
  manager.startAuthentication()
  manager.addOutput('line one')
  manager.setError('first failure')
  manager.setError('latest failure')
  manager.endAuthentication(false)
  const retained = manager.getStatus()
  retained.output.push('mutating the returned copy')
  manager.startAuthentication()
  manager.addOutput('fresh line')
  manager.endAuthentication(true)
  first(); third?.(); dispose()
  manager.startAuthentication()
  process.stdout.write(JSON.stringify({ events, visits, status: manager.getStatus() }))
}
