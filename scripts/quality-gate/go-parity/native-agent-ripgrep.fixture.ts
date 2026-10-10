import * as children from 'node:child_process'
import { join, resolve, sep } from 'node:path'
import { syncBuiltinESMExports } from 'node:module'
import { spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const originalSpawnSync = children.spawnSync
const originalBunSpawnSync = Bun.spawnSync.bind(Bun)
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Agent ripgrep')
if (process.env.CC_HAHA_NATIVE_AGENT_RIPGREP_FIXTURE !== '1') throw new Error('Missing ripgrep fixture')
const capabilities=await installClientFixtureCapabilities(input.homeDir,{absentPaths:absentManagedSettingsFixturePaths()})
// Grant only this fixture's actual temporary executable and --version probe.
const allowed=(file: string,args: string[],opts: any) => {
 const path=resolve(file)
 if (!path.startsWith(resolve(input.homeDir)+sep) || args.length !== 1 || args[0] !== '--version') throw new Error('Unsafe ripgrep subprocess')
 return originalSpawnSync(file,args,{...opts,env:{HOME:input.homeDir,CLAUDE_CONFIG_DIR:join(input.homeDir,'.claude'),PATH:process.env.PATH},cwd:input.homeDir})
}
Bun.spawnSync = ((...args: any[]) => {
 const command=Array.isArray(args[0]) ? args[0] : args[0].cmd
 if (!Array.isArray(command) || command.length !== 2 || command[1] !== '--version' || !resolve(command[0]).startsWith(resolve(input.homeDir)+sep)) throw new Error('Unsafe native ripgrep subprocess')
 return (originalBunSpawnSync as any)(...args)
}) as typeof Bun.spawnSync
require('node:child_process').spawnSync=allowed
spyOn(children,'spawnSync').mockImplementation(allowed as any);syncBuiltinESMExports()
Object.defineProperty(process,'execPath',{value:input.executable,configurable:true})
await import('../../../preload')
const { ripgrepCommand }=await import('../../../src/utils/ripgrep')
process.stdout.write(JSON.stringify({config:ripgrepCommand(),capabilities}))
