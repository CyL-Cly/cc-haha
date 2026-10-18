import path from 'node:path'

/**
 * Shared code-signing identity resolution for the macOS build.
 *
 * WHY THIS EXISTS
 * ---------------
 * The native Computer Use helper refuses commands unless its caller chain is
 * cryptographically the desktop app: `ClientAttestation.swift` requires the
 * host (`com.claude-code-haha.desktop`), the sidecar
 * (`com.claude-code-haha.desktop.sidecar`) and the helper
 * (`dev.cchaha.cu-helper`) to share one signing certificate — same team, same
 * leaf. If any link is ad-hoc or signed by a different cert, every helper call
 * returns `unauthorized_client` and Computer Use is dead in the water.
 *
 * Three separate build steps produce those three binaries (electron-builder,
 * `build-sidecars.ts`, `native/cu-helper/build.sh`), so they need one shared
 * answer to "which certificate?". That answer lives here.
 *
 * Preference order — Developer ID FIRST, deliberately:
 *   1. `CC_HAHA_SIGN_IDENTITY` — explicit override, trusted verbatim.
 *   2. `Developer ID Application: …` — long-lived, notarizable, distributable.
 *      TCC grants are keyed to the signing identity, so a cert that does not
 *      expire yearly is what keeps the user's Accessibility + Screen Recording
 *      grants alive across rebuilds.
 *   3. `Apple Development: …` — works, but expires in about a year, and the
 *      replacement cert silently invalidates every TCC grant.
 *   4. none → caller decides (ad-hoc for local unsigned builds).
 *
 * `native/cu-helper/build.sh` mirrors this order in shell; keep the two in sync.
 */

/** A code-signing identity's full common name, as `codesign --sign` wants it. */
export type SigningIdentity = string

export function codesignTimestampArgument(identity: SigningIdentity | null): '--timestamp' | '--timestamp=none' {
  return identity?.startsWith('Developer ID Application:')
    ? '--timestamp'
    : '--timestamp=none'
}

/** The fixed code-signing identifier the helper's attestation policy expects. */
export const SIDECAR_SIGNING_IDENTIFIER = 'com.claude-code-haha.desktop.sidecar'

/**
 * Pick a stable signing identity out of `security find-identity -v -p codesigning`
 * output. Pure so the preference order is unit-testable without a keychain.
 *
 * @param securityOutput raw stdout of `security find-identity -v -p codesigning`
 * @param override value of `CC_HAHA_SIGN_IDENTITY`, if set
 * @returns the identity's common name, or null when nothing stable is available
 */
export function resolveStableSigningIdentity(
  securityOutput: string,
  override?: string | null,
  requireMatchingOverride = false,
): SigningIdentity | null {
  const rows: Array<{ hash: string; name: string }> = []
  for (const line of securityOutput.split('\n')) {
    const match = /^\s*\d+\)\s+([0-9A-F]{40})\s+"(.+)"\s*$/i.exec(line)
    if (match) rows.push({ hash: match[1].toUpperCase(), name: match[2] })
  }

  const explicit = override?.trim()
  if (explicit) {
    if (requireMatchingOverride) {
      const matched = rows.find(row => row.name === explicit || row.hash === explicit.toUpperCase())
      if (!matched) throw new Error('Signing identity is absent from the explicitly selected keychain')
      return matched.name
    }
    // `codesign --sign` accepts a SHA-1 fingerprint, but downstream timestamp
    // policy needs the certificate kind. Resolve a matching hash to its common
    // name so Developer ID sidecars cannot accidentally ship without a secure
    // timestamp. Unknown overrides remain trusted verbatim and will fail at
    // codesign if they truly do not exist.
    if (/^[0-9A-F]{40}$/i.test(explicit)) {
      return rows.find(row => row.hash === explicit.toUpperCase())?.name ?? explicit
    }
    return explicit
  }

  // Rows look like:  1) <40-hex-sha> "Developer ID Application: Name (TEAMID)"
  // Only the quoted common name is meaningful to `codesign --sign`.
  const names = rows.map(row => row.name)

  return (
    names.find(name => name.startsWith('Developer ID Application:')) ??
    names.find(name => name.startsWith('Apple Development:')) ??
    null
  )
}

/** Resolve an opt-in private keychain without touching filesystem or search lists. */
function signingKeychain(env: NodeJS.ProcessEnv = process.env): string | undefined {
  const keychain = env.CC_HAHA_SIGN_KEYCHAIN
  if (!keychain) return undefined
  if (!path.isAbsolute(keychain)) throw new Error('CC_HAHA_SIGN_KEYCHAIN must be an absolute path')
  return keychain
}

/** Limit every signing operation to the caller's explicit private keychain. */
export function codesignKeychainArguments(env: NodeJS.ProcessEnv = process.env): string[] {
  const keychain = signingKeychain(env)
  return keychain ? ['--keychain', keychain] : []
}

/** Default lookup retains its nullable result; an explicit keychain fails closed. */
export async function detectStableSigningIdentity(): Promise<SigningIdentity | null> {
  if (process.platform !== 'darwin') return null
  const keychain = signingKeychain()
  try {
    const proc = Bun.spawn(
      ['security', 'find-identity', '-v', '-p', 'codesigning', ...(keychain ? [keychain] : [])],
      { stdout: 'pipe', stderr: 'ignore' },
    )
    const stdout = await new Response(proc.stdout).text()
    const exitCode = await proc.exited
    if (keychain && exitCode !== 0) throw new Error('Explicit signing keychain identity query failed')
    const identity = resolveStableSigningIdentity(stdout, process.env.CC_HAHA_SIGN_IDENTITY, !!keychain)
    if (keychain && !identity) throw new Error('Explicit signing keychain has no supported signing identity')
    return identity
  } catch (error) {
    if (keychain) throw error
    return null
  }
}

/**
 * Sign a compiled sidecar with the build's stable identity and — critically —
 * the FIXED identifier the helper's attestation policy expects.
 *
 * The identifier is the reason this is not just `codesign -s -`. Ad-hoc signing
 * derives the identifier from the file name plus a content hash
 * (`claude-sidecar-aarch64-apple-darwin-5555…`), which never matches
 * `com.claude-code-haha.desktop.sidecar`. `ClientAttestation.swift` compares
 * that identifier exactly, so a hash-suffixed one makes every Computer Use call
 * fail closed with `unauthorized_client`.
 *
 * `--identifier` applies to ad-hoc signatures too, so the unsigned local build
 * still gets the right identifier — it just cannot satisfy the team/leaf half of
 * the policy, which is inherent to having no certificate.
 *
 * electron-builder must not overwrite this signature; `mac.signIgnore` in
 * package.json excludes `claude-sidecar-…` for exactly that reason. That also
 * means entitlements are OUR job here — electron-builder's `entitlementsInherit`
 * never reaches a file it does not sign.
 */
export async function signMacBinary(outputPath: string, desktopRoot: string, options: { adHocOnly?: boolean } = {}) {
  if (options.adHocOnly && (process.env.CC_HAHA_SIGN_IDENTITY || process.env.CC_HAHA_SIGN_KEYCHAIN)) {
    throw new Error('Explicit ad-hoc signing cannot be combined with a signing identity or keychain override')
  }
  const identity = options.adHocOnly ? null : await detectStableSigningIdentity()
  const keychainArgs = options.adHocOnly ? [] : codesignKeychainArguments()
  const label = identity ?? 'ad-hoc'
  console.log(
    `[build-sidecars] signing ${outputPath} as ${SIDECAR_SIGNING_IDENTIFIER} (${label}) ...`,
  )

  const strip = Bun.spawn(['codesign', '--remove-signature', outputPath], {
    stdout: 'inherit',
    stderr: 'inherit',
  })
  await strip.exited

  const args = [
    'codesign',
    '--sign',
    identity ?? '-',
    '--force',
    '--identifier',
    SIDECAR_SIGNING_IDENTIFIER,
    codesignTimestampArgument(identity),
    ...keychainArgs,
  ]
  if (identity) {
    // Hardened runtime + inherited entitlements match what electron-builder
    // would have applied, so skipping its signing pass changes nothing else
    // about how the sidecar runs.
    args.push(
      '--options',
      'runtime',
      '--entitlements',
      path.join(desktopRoot, 'build', 'entitlements.mac.inherit.plist'),
    )
  }
  args.push(outputPath)

  const sign = Bun.spawn(args, { stdout: 'inherit', stderr: 'inherit' })
  const signExit = await sign.exited
  if (signExit !== 0) {
    throw new Error(
      `[build-sidecars] codesign failed for ${outputPath} (exit ${signExit}, identity: ${label})`,
    )
  }
  console.log(`[build-sidecars] signed ${outputPath} (${label})`)
}
