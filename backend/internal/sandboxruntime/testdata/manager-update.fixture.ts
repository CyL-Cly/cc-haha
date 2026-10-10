import {SandboxManager} from '@anthropic-ai/sandbox-runtime'
const input=JSON.parse(await Bun.stdin.text())
SandboxManager.updateConfig(input.config)
input.config.network.allowedDomains.push('injected.example')
input.config.network.allowUnixSockets[0]='changed'
input.config.network.allowLocalBinding=false
input.config.filesystem.allowWrite[0]='changed'
input.config.ignoreViolations['*'].push('changed')
input.config.ripgrep.args.push('changed')
input.config.enableWeakerNetworkIsolation=false
process.stdout.write(JSON.stringify(SandboxManager.getConfig())+'\n')
