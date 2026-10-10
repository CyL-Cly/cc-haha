export type TLSConfigOperation =
  | { op: 'env'; value: Record<string, string> }
  | { op: 'write'; name: string; hex: string }
  | { op: 'mkdir' | 'remove'; name: string }
  | { op: 'get' | 'clear' }
  | { op: 'mutate'; action: 'push' | 'set' | 'pop'; index?: number; value?: string }
  | { op: 'option'; options: string; flag: string }
export type TLSConfigCase = { name: string; operations: TLSConfigOperation[] }
const env = (value: Record<string, string>): TLSConfigOperation => ({ op: 'env', value })
const write = (name: string, text: string): TLSConfigOperation => ({ op: 'write', name, hex: Buffer.from(text).toString('hex') })
const get: TLSConfigOperation = { op: 'get' }, clear: TLSConfigOperation = { op: 'clear' }
export const tlsConfigCases: TLSConfigCase[] = [
  { name: 'default-undefined-cached', operations: [get, get, env({ NODE_EXTRA_CA_CERTS: 'file:a' }), write('a', 'later'), get, clear, get] },
  { name: 'empty-extra-env-is-undefined', operations: [env({ NODE_EXTRA_CA_CERTS: '' }), get] },
  { name: 'extra-is-one-raw-item', operations: [write('a', 'first\nsecond\n'), env({ NODE_EXTRA_CA_CERTS: 'file:a' }), get, get] },
  { name: 'empty-file-is-an-item', operations: [write('a', ''), env({ NODE_EXTRA_CA_CERTS: 'file:a' }), get] },
  { name: 'bom-and-newlines-preserved', operations: [write('a', '\ufeffone\r\ntwo\n'), env({ NODE_EXTRA_CA_CERTS: 'file:a' }), get] },
  { name: 'utf8-replacement-incomplete', operations: [{ op: 'write', name: 'a', hex: 'efbbbff09f92ed a080c0afe282'.replaceAll(' ', '') }, env({ NODE_EXTRA_CA_CERTS: 'file:a' }), get] },
  { name: 'unicode-astral-nul', operations: [write('a', '证书😀\u0000\n'), env({ NODE_EXTRA_CA_CERTS: 'file:a' }), get] },
  { name: 'file-change-stale-until-clear', operations: [write('a', 'before'), env({ NODE_EXTRA_CA_CERTS: 'file:a' }), get, write('a', 'after'), get, clear, get] },
  { name: 'env-change-stale-until-clear', operations: [write('a', 'A'), write('b', 'B'), env({ NODE_EXTRA_CA_CERTS: 'file:a' }), get, env({ NODE_EXTRA_CA_CERTS: 'file:b' }), get, clear, get] },
  { name: 'deleted-file-cached-then-read-fails', operations: [write('a', 'A'), env({ NODE_EXTRA_CA_CERTS: 'file:a' }), get, { op: 'remove', name: 'a' }, get, clear, get] },
  { name: 'missing-file-fallback-cached', operations: [env({ NODE_EXTRA_CA_CERTS: 'file:a' }), get, write('a', 'later'), get, clear, get] },
  { name: 'directory-read-fallback', operations: [{ op: 'mkdir', name: 'a' }, env({ NODE_EXTRA_CA_CERTS: 'file:a' }), get] },
  { name: 'array-push-reference-until-clear', operations: [write('a', 'A'), env({ NODE_EXTRA_CA_CERTS: 'file:a' }), get, { op: 'mutate', action: 'push', value: 'mutated' }, get, clear, get] },
  { name: 'array-base-change-independent-resource', operations: [write('a', 'A'), env({ NODE_EXTRA_CA_CERTS: 'file:a' }), get, { op: 'mutate', action: 'set', index: 0, value: 'changed-base' }, get, clear, get] },
  { name: 'array-pop-reference', operations: [write('a', 'A'), env({ NODE_EXTRA_CA_CERTS: 'file:a' }), get, { op: 'mutate', action: 'pop' }, get, clear, get] },
  { name: 'array-lone-utf16-preserved', operations: [write('a', 'A'), env({ NODE_EXTRA_CA_CERTS: 'file:a' }), get, { op: 'mutate', action: 'push', value: '\ud800\udfff\ud800' }, get] },
  { name: 'array-shadow-pem-like-text', operations: [write('a', '-----BEGIN CERTIFICATE-----\njunk\n-----END CERTIFICATE-----\n'), env({ NODE_EXTRA_CA_CERTS: 'file:a' }), get] },
  { name: 'cached-nil-ignores-later-system-option', operations: [get, env({ NODE_OPTIONS: '--use-system-ca' }), get] },
  { name: 'cached-array-ignores-later-system-option', operations: [write('a', 'A'), env({ NODE_EXTRA_CA_CERTS: 'file:a' }), get, env({ NODE_OPTIONS: '--use-openssl-ca' }), get] },
  { name: 'extra-whitespace-path-is-truthy', operations: [env({ NODE_EXTRA_CA_CERTS: ' ' }), get] },
  ...[
    ['', '--use-system-ca'], [' --use-system-ca\t--other\n', '--use-system-ca'], ['--use-openssl-ca', '--use-openssl-ca'],
    ['--use-system-ca=yes', '--use-system-ca'], ['"--use-system-ca"', '--use-system-ca'], ['--use-system-ca\u0085next', '--use-system-ca'],
    ['\ufeff--use-system-ca\u00a0next', '--use-system-ca'], ['\u2028--use-system-ca\u2029', '--use-system-ca'],
    ['\u1680--use-system-ca\u2000', '--use-system-ca'], ['--other --use-openssl-ca --other', '--use-system-ca'],
  ].map(([options, flag], index): TLSConfigCase => ({ name: 'node-options-' + index, operations: [{ op: 'option', options: options!, flag: flag! }] })),
]
