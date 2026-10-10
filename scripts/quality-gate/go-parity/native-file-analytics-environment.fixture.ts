import { mock } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
assertDisposableFixtureHome(process.env.HOME, 'Analytics environment actual Source')
const input = await Bun.stdin.json()
const absent = () => { throw Object.assign(new Error('Isolated absent host fact'), { code: 'ENOENT' }) }
mock.module('../../../src/utils/fsOperations', () => ({ getFsImplementation: () => ({ readFileSync: absent, existsSync: () => false }) }))
const output = []
for (let i = 0; i < input.cases.length; i++) {
  for (const key of input.keys) delete process.env[key]
  Object.assign(process.env, input.cases[i])
  const source = await import(`../../../src/utils/env.ts?analytics-environment-profile=${i}`)
  output.push({ terminal: source.env.terminal, deployment: source.detectDeploymentEnvironment() })
}
process.stdout.write(JSON.stringify(output))
