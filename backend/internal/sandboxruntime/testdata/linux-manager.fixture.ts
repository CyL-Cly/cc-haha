import { SandboxManager } from '@anthropic-ai/sandbox-runtime'
const input = JSON.parse(await Bun.stdin.text())
process.chdir(input.cwd)
Object.defineProperty(process, 'platform', { value: 'linux' })
const rows = []
try {
  await SandboxManager.initialize(input.configs[0])
  for (const [index,config] of input.configs.entries()) {
    process.env.CC_HAHA_SANDBOX_ENVIRONMENT_PROBE = String(index)
    SandboxManager.updateConfig(config)
    rows.push({
      command: await SandboxManager.wrapWithSandbox(input.command, '/bin/bash'),
      http: SandboxManager.getLinuxHttpSocketPath(),
      socks: SandboxManager.getLinuxSocksSocketPath(),
    })
  }
} finally {
  await SandboxManager.reset()
}
process.stdout.write(JSON.stringify(rows) + '\n')
