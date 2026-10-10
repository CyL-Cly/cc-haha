import { assertDisposableFixtureHome } from './fixture-guard'
import { mkdir, readFile, stat, writeFile } from 'node:fs/promises'
import { join } from 'node:path'

assertDisposableFixtureHome(process.env.HOME, 'Native DingTalk unbind routes')
if (process.env.CC_HAHA_DINGTALK_UNBIND_FIXTURE !== '1') throw new Error('Unowned DingTalk unbind fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const { handleAdaptersApi } = await import('../../../src/server/api/adapters')
await mkdir(process.env.CLAUDE_CONFIG_DIR!, { recursive: true })
const file = join(process.env.CLAUDE_CONFIG_DIR!, 'adapters.json')
const output = []
for (const row of input.rows) {
  await writeFile(file, JSON.stringify(row.seed), { mode: 0o600 })
  const url = new URL(`http://private.test/api/adapters/dingtalk/${row.tail.join('/')}`)
  const response = await handleAdaptersApi(new Request(url, {
    method: row.method,
    ...(row.body === undefined ? {} : { body: JSON.stringify(row.body) }),
  }), url, ['api', 'adapters', 'dingtalk', ...row.tail])
  output.push({ status: response.status, body: await response.json(), raw: JSON.parse(await readFile(file, 'utf8')), mode: (await stat(file)).mode & 0o777 })
}
process.stdout.write(JSON.stringify(output))
