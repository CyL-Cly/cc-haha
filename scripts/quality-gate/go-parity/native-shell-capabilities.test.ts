import { expect, test } from 'bun:test'
import { mkdtemp, rm, stat } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'

const executable = '/opt/homebrew/bin/pwsh'
const allocationRoot = tmpdir()
const fixture = fileURLToPath(new URL('./native-shell-capabilities.fixture.ts', import.meta.url))
for (const enabled of [false, true]) {
  test(`native shell exact executable capability, grant=${enabled}`, async () => {
    try { await stat(executable) } catch { return }
    const home = await mkdtemp(join(allocationRoot, 'native-shell-capabilities-'))
    let child: ReturnType<typeof Bun.spawn> | undefined
    try {
      child = Bun.spawn([process.execPath, '--no-env-file', fixture], { cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_FIXTURE_TEMP_ROOT: allocationRoot }), stdin: new Blob([JSON.stringify({ home, executable, enabled })]), stdout: 'pipe', stderr: 'pipe' })
      const timer = setTimeout(() => child?.kill('SIGKILL'), 15_000)
      try {
        const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
        expect(code, stderr).toBe(0)
        expect(stderr).toBe('')
        expect(JSON.parse(stdout)).toEqual({ observations: [...(enabled ? ['owned-pid-query', 'physical-spawn', 'physical-owned-fd', 'closed-fd'] : ['default-external-read', 'default-spawn']), 'external-directory', 'external-write', 'other-executable', 'other-args', 'other-exec-api', 'arbitrary-shell', 'inherited-environment', 'foreign-home', 'foreign-cwd', 'unowned-fd', 'native-spawn', 'foreign-pid-query', 'other-query-args'], blocked: enabled ? 14 : 15 })
      } finally { clearTimeout(timer) }
    } finally { child?.kill(); if (child) await child.exited; await rm(home, { recursive: true, force: true }) }
  }, 20_000)
}
