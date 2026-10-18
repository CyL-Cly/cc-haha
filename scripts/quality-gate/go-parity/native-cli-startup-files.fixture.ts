import { assertDisposableFixtureHome } from './fixture-guard'
import { spyOn } from 'bun:test'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.home, 'CLI startup file Source')
if (process.env.CC_HAHA_CLI_FILE_FIXTURE !== '1') throw new Error('Unowned CLI file fixture')
const { installClientFixtureCapabilities, absentManagedSettingsFixturePaths } = await import('./client-options-capabilities')
const capabilities = await installClientFixtureCapabilities(input.home, { absentPaths: absentManagedSettingsFixturePaths(), loopbackFetchOrigin: new URL(input.base).origin })
// Replace only Axios's HTTP primitive; production Files API parsing, retry,
// headers, concurrency and filesystem consumers still execute unchanged.
const { default: axios } = await import('axios')
spyOn(axios, 'get').mockImplementation(async (url: string, options: any) => {
  const response = await fetch(url, { headers: options.headers })
  return { status: response.status, data: Buffer.from(await response.arrayBuffer()) } as any
})
const { setCwdState } = await import('../../../src/bootstrap/state')
setCwdState(input.cwd)
const { parseFileSpecs, buildDownloadPath, downloadSessionFiles } = await import('../../../src/services/api/filesApi')
const files = parseFileSpecs(input.specs)
const paths = input.paths.map((relative: string) => buildDownloadPath(input.cwd, input.sid, relative))
const results = await downloadSessionFiles(files, { baseUrl: input.base, oauthToken: 'source-ingress-token', sessionId: input.sid })
process.stdout.write(JSON.stringify({ files, paths, results, blocked: capabilities.blocked }))
