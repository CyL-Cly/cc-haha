import { afterEach, describe, expect, it, vi } from 'vitest'
import * as signing from './sign-identity'

import {
  SIDECAR_SIGNING_IDENTIFIER,
  codesignTimestampArgument,
  resolveStableSigningIdentity,
} from './sign-identity'

/** Realistic `security find-identity -v -p codesigning` output. */
const BOTH_IDENTITIES = `  1) 6AB17527A9E2AEB0C596F8D9F7728D215A5C0E57 "Apple Development: dev@example.com (F8ZSJJ78S7)"
  2) 5145958D6E31AD0CD6BBACD804A0B357E3CEDEA7 "Developer ID Application: Example Co., Ltd (D3RS24869F)"
     2 valid identities found`

describe('resolveStableSigningIdentity', () => {
  it('prefers Developer ID even when Apple Development is listed first', () => {
    // TCC grants are keyed to the signing identity. Apple Development certs
    // expire in about a year, and the replacement silently drops the user's
    // Accessibility + Screen Recording grants — so listing order must not
    // decide this.
    expect(resolveStableSigningIdentity(BOTH_IDENTITIES)).toBe(
      'Developer ID Application: Example Co., Ltd (D3RS24869F)',
    )
  })

  it('falls back to Apple Development when no Developer ID exists', () => {
    const onlyDev = `  1) 6AB17527A9E2AEB0C596F8D9F7728D215A5C0E57 "Apple Development: dev@example.com (F8ZSJJ78S7)"
     1 valid identities found`
    expect(resolveStableSigningIdentity(onlyDev)).toBe(
      'Apple Development: dev@example.com (F8ZSJJ78S7)',
    )
  })

  it('returns null when the keychain has no code-signing identity', () => {
    expect(resolveStableSigningIdentity('     0 valid identities found')).toBeNull()
    expect(resolveStableSigningIdentity('')).toBeNull()
  })

  it('honours an explicit override verbatim, ahead of auto-detection', () => {
    // The override is how CI pins a specific cert, and how build-macos-arm64.sh
    // hands the SAME identity to the sidecar and the helper.
    expect(
      resolveStableSigningIdentity(BOTH_IDENTITIES, 'Developer ID Application: Other (AAAAAAAAAA)'),
    ).toBe('Developer ID Application: Other (AAAAAAAAAA)')
  })

  it('resolves a SHA-1 override to its Developer ID common name', () => {
    expect(
      resolveStableSigningIdentity(
        BOTH_IDENTITIES,
        '5145958D6E31AD0CD6BBACD804A0B357E3CEDEA7',
      ),
    ).toBe('Developer ID Application: Example Co., Ltd (D3RS24869F)')
  })

  it('ignores a blank override rather than treating it as "no identity"', () => {
    // An unset env var arrives as '' or a stray space; that must not suppress
    // auto-detection and silently produce an ad-hoc build.
    expect(resolveStableSigningIdentity(BOTH_IDENTITIES, '')).toBe(
      'Developer ID Application: Example Co., Ltd (D3RS24869F)',
    )
    expect(resolveStableSigningIdentity(BOTH_IDENTITIES, '   ')).toBe(
      'Developer ID Application: Example Co., Ltd (D3RS24869F)',
    )
    expect(resolveStableSigningIdentity(BOTH_IDENTITIES, null)).toBe(
      'Developer ID Application: Example Co., Ltd (D3RS24869F)',
    )
  })

  it('ignores identity types that cannot satisfy the helper attestation', () => {
    // A self-signed cert has no team identifier, so the helper's
    // validSignerChain() can never match it against the host and sidecar.
    const selfSigned = `  1) 1111111111111111111111111111111111111111 "cu-helper-dev"
     1 valid identities found`
    expect(resolveStableSigningIdentity(selfSigned)).toBeNull()
  })

  it('does not mistake certificate names for the quoted-name column', () => {
    // Defensive: a cert whose name merely contains a quote-like fragment must
    // not shift which text is treated as the identity.
    const odd = `  1) 2222222222222222222222222222222222222222 "Developer ID Application: A "B" Co (TEAMID1234)"
     1 valid identities found`
    expect(resolveStableSigningIdentity(odd)).toBe(
      'Developer ID Application: A "B" Co (TEAMID1234)',
    )
  })
})

describe('SIDECAR_SIGNING_IDENTIFIER', () => {
  it('matches the identifier ClientAttestation.swift compares against', () => {
    // These two constants are a cross-language contract with no compiler to
    // enforce it: ClientAttestation.swift:26 and cuHelperInstall.ts:46 both
    // hard-code this exact string, and the helper rejects every Computer Use
    // call when the sidecar's real identifier differs.
    expect(SIDECAR_SIGNING_IDENTIFIER).toBe('com.claude-code-haha.desktop.sidecar')
  })
})

describe('codesignTimestampArgument', () => {
  it('requires a secure timestamp for Developer ID distribution', () => {
    expect(codesignTimestampArgument('Developer ID Application: Example (TEAMID1234)'))
      .toBe('--timestamp')
  })

  it('keeps local development and ad-hoc signing offline', () => {
    expect(codesignTimestampArgument('Apple Development: Example (TEAMID1234)'))
      .toBe('--timestamp=none')
    expect(codesignTimestampArgument(null)).toBe('--timestamp=none')
  })
})


const platformDescriptor = Object.getOwnPropertyDescriptor(process, 'platform')!
afterEach(() => {
  Object.defineProperty(process, 'platform', platformDescriptor)
  vi.unstubAllEnvs()
  vi.unstubAllGlobals()
})

function mockSigningCommands(output = BOTH_IDENTITIES, queryExit = 0) {
  Object.defineProperty(process, 'platform', { ...platformDescriptor, value: 'darwin' })
  const spawn = vi.fn((command: string[]) => ({
    stdout: command[0] === 'security' ? output : '',
    exited: Promise.resolve(command[0] === 'security' ? queryExit : 0),
  }))
  vi.stubGlobal('Bun', { spawn })
  vi.stubEnv('CC_HAHA_SIGN_KEYCHAIN', '/fixture/private signing.keychain-db')
  vi.stubEnv('CC_HAHA_SIGN_IDENTITY', '5145958D6E31AD0CD6BBACD804A0B357E3CEDEA7')
  return spawn
}

describe('explicit private signing keychain', () => {
  it('scopes override lookup to only the explicit private keychain', async () => {
    const spawn = mockSigningCommands()
    await expect(signing.detectStableSigningIdentity()).resolves.toBe(
      'Developer ID Application: Example Co., Ltd (D3RS24869F)',
    )
    expect(spawn.mock.calls.map(([command]) => command)).toEqual([
      ['security', 'find-identity', '-v', '-p', 'codesigning', '/fixture/private signing.keychain-db'],
    ])
  })

  it('rejects an override absent from the private query instead of searching defaults', async () => {
    const spawn = mockSigningCommands()
    vi.stubEnv('CC_HAHA_SIGN_IDENTITY', 'Apple Development: Outside (OTHER12345)')
    await expect(signing.detectStableSigningIdentity()).rejects.toThrow('absent from the explicitly selected keychain')
    expect(spawn).toHaveBeenCalledTimes(1)
    expect(spawn.mock.calls[0][0].at(-1)).toBe('/fixture/private signing.keychain-db')
  })

  it.each([
    ['empty identity set', '     0 valid identities found', 0],
    ['failed query', BOTH_IDENTITIES, 1],
  ])('fails closed for %s before any codesign', async (_label, output, code) => {
    const spawn = mockSigningCommands(output, code)
    await expect(signing.signMacBinary('/fixture/sidecar', '/fixture/desktop')).rejects.toThrow()
    expect(spawn.mock.calls.map(([command]) => command[0])).toEqual(['security'])
  })

  it('rejects a relative keychain before calling any native command', async () => {
    const spawn = mockSigningCommands()
    vi.stubEnv('CC_HAHA_SIGN_KEYCHAIN', 'relative.keychain-db')
    await expect(signing.detectStableSigningIdentity()).rejects.toThrow('must be an absolute path')
    expect(spawn).not.toHaveBeenCalled()
  })

  it('scopes sidecar signing while retaining artifact-only removal, fixed identity and entitlements', async () => {
    const spawn = mockSigningCommands()
    await signing.signMacBinary('/fixture/sidecar', '/fixture/desktop')
    const commands = spawn.mock.calls.map(([command]) => command)
    expect(commands).toHaveLength(3)
    expect(commands[0]).toEqual(['security', 'find-identity', '-v', '-p', 'codesigning', '/fixture/private signing.keychain-db'])
    expect(commands[1]).toEqual(['codesign', '--remove-signature', '/fixture/sidecar'])
    expect(commands[2]).toEqual([
      'codesign', '--sign', 'Developer ID Application: Example Co., Ltd (D3RS24869F)',
      '--force', '--identifier', SIDECAR_SIGNING_IDENTIFIER, '--timestamp',
      '--keychain', '/fixture/private signing.keychain-db', '--options', 'runtime',
      '--entitlements', '/fixture/desktop/build/entitlements.mac.inherit.plist', '/fixture/sidecar',
    ])
  })

  it('retains ordinary ad-hoc sidecar builds when default discovery finds no identity', async () => {
    const spawn = mockSigningCommands('     0 valid identities found')
    vi.stubEnv('CC_HAHA_SIGN_KEYCHAIN', undefined)
    vi.stubEnv('CC_HAHA_SIGN_IDENTITY', undefined)
    await signing.signMacBinary('/fixture/sidecar', '/fixture/desktop')
    const commands = spawn.mock.calls.map(([command]) => command)
    expect(commands).toHaveLength(3)
    expect(commands[2]).toEqual([
      'codesign', '--sign', '-', '--force', '--identifier', SIDECAR_SIGNING_IDENTIFIER,
      '--timestamp=none', '/fixture/sidecar',
    ])
  })

  it('retains the original unscoped trusted override behavior when no keychain is selected', async () => {
    const spawn = mockSigningCommands()
    vi.stubEnv('CC_HAHA_SIGN_KEYCHAIN', undefined)
    vi.stubEnv('CC_HAHA_SIGN_IDENTITY', 'Apple Development: Outside (OTHER12345)')
    await expect(signing.detectStableSigningIdentity()).resolves.toBe('Apple Development: Outside (OTHER12345)')
    expect(spawn.mock.calls.map(([command]) => command)).toEqual([
      ['security', 'find-identity', '-v', '-p', 'codesigning'],
    ])
    expect(signing.codesignKeychainArguments()).toEqual([])
  })
})


describe('explicit non-CU ad-hoc signing', () => {
  it('does not query any keychain and retains the fixed sidecar identity', async () => {
    const spawn = mockSigningCommands()
    vi.stubEnv('CC_HAHA_SIGN_IDENTITY', undefined)
    vi.stubEnv('CC_HAHA_SIGN_KEYCHAIN', undefined)
    await signing.signMacBinary('/fixture/sidecar', '/fixture/desktop', { adHocOnly: true })
    expect(spawn.mock.calls.map(([command]) => command)).toEqual([
      ['codesign', '--remove-signature', '/fixture/sidecar'],
      ['codesign', '--sign', '-', '--force', '--identifier', SIDECAR_SIGNING_IDENTIFIER,
        '--timestamp=none', '/fixture/sidecar'],
    ])
  })

  it.each(['CC_HAHA_SIGN_IDENTITY', 'CC_HAHA_SIGN_KEYCHAIN'])(
    'rejects a conflicting explicit %s before any native command', async variable => {
      const spawn = mockSigningCommands()
      vi.stubEnv('CC_HAHA_SIGN_IDENTITY', undefined)
      vi.stubEnv('CC_HAHA_SIGN_KEYCHAIN', undefined)
      vi.stubEnv(variable, 'explicit-conflicting-value')
      await expect(signing.signMacBinary('/fixture/sidecar', '/fixture/desktop', { adHocOnly: true }))
        .rejects.toThrow('cannot be combined')
      expect(spawn).not.toHaveBeenCalled()
    },
  )
})
