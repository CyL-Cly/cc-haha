import { expect, test } from 'bun:test'
import { mkdtemp, rm, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { tmpdir } from 'node:os'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { X509Certificate } from 'node:crypto'

const root = fileURLToPath(new URL('../../../', import.meta.url)), allocationRoot = tmpdir()
// A public, deliberately private test trust anchor and its matching fixture
// key. They are never consulted by production or a real provider connection.
const certificate = await Bun.file(new URL('./testdata/private-loopback-certificate.pem', import.meta.url)).text()
const key = await Bun.file(new URL('./testdata/private-loopback-key.pem', import.meta.url)).text()

for (const mode of ['success', 'foreign-origin', 'downgrade', 'redirect', 'filesystem', 'native', 'public-origin', 'path-origin', 'userinfo-origin', 'port-zero']) {
  test(`client HTTPS exact-origin capability: ${mode}`, async () => {
    const home = await mkdtemp(join(allocationRoot, 'client-https-capability-'))
    let child: ReturnType<typeof Bun.spawn> | undefined
    let firstCount = 0, secondCount = 0
    const second = Bun.serve({ hostname: '127.0.0.1', port: 0, tls: { cert: certificate, key }, fetch: () => { secondCount++; return new Response('escaped') } })
    const first = Bun.serve({ hostname: '127.0.0.1', port: 0, tls: { cert: certificate, key }, fetch: () => {
      firstCount++
      return mode === 'redirect' ? Response.redirect(`https://127.0.0.1:${second.port}/redirected`) : new Response('private verified response')
    } })
    try {
      await writeFile(join(home, 'certificate.pem'), certificate)
      const origin = `https://127.0.0.1:${first.port}`
      const grant = mode === 'public-origin' ? 'https://api.deepseek.com:8443' : mode === 'path-origin' ? origin+'/path' : mode === 'userinfo-origin' ? `https://fake@127.0.0.1:${first.port}` : mode === 'port-zero' ? 'https://127.0.0.1:0' : origin
      const destination = mode === 'foreign-origin' ? `https://127.0.0.1:${second.port}` : mode === 'downgrade' ? origin.replace('https:', 'http:') : origin+'/verified'
      child = Bun.spawn([process.execPath, '--no-env-file', join(root, 'scripts/quality-gate/go-parity/client-https-capability.fixture.ts')], {
        cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_CLIENT_HTTPS_CAPABILITY: '1', CC_HAHA_FIXTURE_TEMP_ROOT: allocationRoot }),
        stdin: new Blob([JSON.stringify({ homeDir: home, origin: grant, destination, mode })]), stdout: 'pipe', stderr: 'pipe',
      })
      const timer = setTimeout(() => child?.kill('SIGKILL'), 15_000)
      try {
        const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
        if (mode === 'success') {
          expect(code, stderr).toBe(0)
          expect(stderr).toBe('')
          expect(JSON.parse(stdout)).toEqual({ status: 200, body: 'private verified response', capabilities: { blocked: [], secureReads: [], absentReads: [] } })
          expect(firstCount).toBe(1)
        } else {
          expect(code).toBe(1)
          expect(stdout).toBe('')
          if (mode === 'redirect') expect(stderr).toContain('UnexpectedRedirect')
          else if (mode.endsWith('-origin') && !['foreign-origin'].includes(mode) || mode === 'port-zero') expect(stderr).toContain('requires an explicit HTTP(S) loopback origin')
          else expect(stderr).toContain('Client options fixture blocked '+(mode === 'filesystem' ? 'filesystem:' : mode === 'native' ? 'native:' : 'network:fetch'))
          expect(firstCount).toBe(mode === 'redirect' ? 1 : 0)
        }
        expect(secondCount).toBe(0)
        expect(new X509Certificate(await Bun.file(join(home, 'certificate.pem')).text()).fingerprint256).toBe(new X509Certificate(certificate).fingerprint256)
      } finally { clearTimeout(timer) }
    } finally { child?.kill(); if (child) await child.exited; first.stop(true); second.stop(true); await rm(home, { recursive: true, force: true }) }
  })
}
