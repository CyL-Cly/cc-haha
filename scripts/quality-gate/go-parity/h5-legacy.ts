import { readFile } from 'node:fs/promises'
import { classifyH5Request, isH5AccessControlPath, isLocalCredentialOnlyPath, isLoopbackHost, normalizeHostname, requiresLocalAccessCredential, resolveTrustedRendererOrigin, shouldBlockDisabledH5Access, shouldRequireH5Token } from '../../../src/server/h5AccessPolicy'
import type { H5FixtureGroup } from './h5-fixtures'

// The reference is the actual TS policy, without a server, provider, or config.
const path = process.env.CC_HAHA_H5_FIXTURE
if (!path) throw new Error('H5 fixture path is required')
const fixture = JSON.parse(await readFile(path, 'utf8')) as H5FixtureGroup
const requests = fixture.requests.map(({ request: input, context }) => {
  try {
    const headers = new Headers()
    for (const [name, values] of Object.entries(input.headers)) for (const value of values) headers.append(name, value)
    const request = new Request(input.url, { method: input.method, headers })
    const url = new URL(request.url)
    return {
      kind: classifyH5Request(request, url, context),
      require: [false, true].map(h5Enabled => shouldRequireH5Token({ request, url, context, h5Enabled })),
      block: [false, true].flatMap(h5Enabled => [false, true].map(explicitAuthRequired => shouldBlockDisabledH5Access({ request, url, context, h5Enabled, explicitAuthRequired }))),
      credential: requiresLocalAccessCredential(url.pathname, context),
      url: { href: url.href, hostname: url.hostname, pathname: url.pathname, origin: url.origin },
    }
  } catch { return { error: true } }
})
const result = {
  requests,
  hosts: (fixture.hosts ?? []).map(host => ({ normalized: normalizeHostname(host), loopback: isLoopbackHost(host) })),
  rendererOrigins: (fixture.rendererOrigins ?? []).map(origin => resolveTrustedRendererOrigin(origin ?? undefined)),
  paths: (fixture.paths ?? []).map(({ pathname, context }) => ({ control: isH5AccessControlPath(pathname), localOnly: isLocalCredentialOnlyPath(pathname), credential: requiresLocalAccessCredential(pathname, context) })),
}
process.stdout.write(`TS_H5_RESULT:${JSON.stringify(result)}\n`)
