import type { AfterPackContext } from 'app-builder-lib'
import { readScopeExclusions } from '../../scripts/quality-gate/scope-exclusions.ts'

/** Keep the user-excluded CU build local without changing release signing. */
export default function afterPack(
  context: AfterPackContext,
  env: NodeJS.ProcessEnv = process.env,
): void {
  const exclusions = readScopeExclusions(env)
  if (context.electronPlatformName !== 'darwin' || !exclusions.includes('computer-use')) return

  Object.assign(context.packager.platformSpecificBuildOptions, {
    timestamp: 'none',
    notarize: false,
  })
}
