export type AuthStep = {
  args?: string[]
  fileEnv?: string | null
  file?: string | number[] | null
  marker?: boolean
  secure?: string | null
  secureError?: boolean
  clear?: boolean
}
export type AuthFixture = { name: string; steps: AuthStep[]; sourceOnly?: boolean }

const loggedIn = '{"accessToken":"fixture-access","refreshToken":"fixture-refresh","expiresAt":1700000000123,"email":"fixture@example.invalid","accountId":"fixture-account"}'
const secure = (token = loggedIn) => `{"openaiCodexOauth":${token},"claudeAiOauth":{"accessToken":"other-provider"}}`
const jsonArgs = ['auth', 'status', '--openai']
const textArgs = [...jsonArgs, '--text']
export const authFixtures: AuthFixture[] = [
  { name: 'missing-credentials', steps: [{ args: jsonArgs }, { args: textArgs }] },
  ...['null', 'false', '0', '-0', '1e-400', '""'].map(value => ({ name: `falsy-secure-token-${value}`, steps: [{ secure: secure(value) }, { args: textArgs }] })),
  { name: 'secure-store-json-and-text', steps: [{ secure: secure(), args: jsonArgs }, { args: textArgs }] },
  { name: 'desktop-file-json-and-text', steps: [{ fileEnv: '$TOKEN_FILE', file: loggedIn }, { args: textArgs }] },
  { name: 'json-flag-and-text-wins-in-either-order', steps: [{ secure: secure(), args: [...jsonArgs, '--json'] }, { args: [...jsonArgs, '--json', '--text'] }, { args: [...jsonArgs, '--text', '--json'] }] },
  { name: 'file-wins-over-secure-and-marker', steps: [{ fileEnv: '  $TOKEN_FILE  ', file: loggedIn, secure: secure('{"accessToken":"other","refreshToken":"other","expiresAt":1,"email":"wrong"}'), marker: true }] },
  { name: 'missing-selected-file-does-not-fallback', steps: [{ fileEnv: '$TOKEN_FILE', secure: secure() }] },
  { name: 'empty-file-env-uses-secure', steps: [{ fileEnv: '', secure: secure() }] },
  { name: 'unicode-trim-empty-file-env-uses-secure', steps: [{ fileEnv: '\ufeff\u00a0\t\n', secure: secure() }] },
  { name: 'marker-prevents-fallback', steps: [{ marker: true, secure: secure() }] },
  { name: 'secure-reader-failure-is-cached', steps: [{ secureError: true }, { secureError: false, secure: secure() }, { clear: true }] },
  { name: 'file-cache-survives-file-edit-until-clear', steps: [{ fileEnv: '$TOKEN_FILE', file: loggedIn }, { file: null }, { clear: true }] },
  { name: 'secure-cache-survives-new-marker-until-clear', steps: [{ secure: secure() }, { marker: true, secure: null }, { clear: true }] },
  { name: 'null-cache-and-different-file-key', steps: [{ fileEnv: '$TOKEN_FILE' }, { file: loggedIn }, { fileEnv: '$TOKEN_FILE_TWO', file: loggedIn }, { fileEnv: '$TOKEN_FILE' }, { clear: true }] },
  { name: 'file-key-then-secure-key-then-file-key', steps: [{ fileEnv: '$TOKEN_FILE', file: loggedIn, secure: secure('{"accessToken":"s","refreshToken":"s","expiresAt":2}') }, { fileEnv: null }, { fileEnv: '$TOKEN_FILE' }] },
  ...['null', '[]', '1', '"token"', '{}', '{"accessToken":1,"refreshToken":"r","expiresAt":1}', '{"accessToken":"a","refreshToken":1,"expiresAt":1}', '{"accessToken":"a","refreshToken":"r","expiresAt":"1"}', '{broken', '\ufeff' + loggedIn].map((file, index) => ({ name: `invalid-desktop-file-${index}`, steps: [{ fileEnv: '$TOKEN_FILE', file, secure: secure() }] })),
  ...['0', '-0', '-1', '1', '0.99', '8640000000000000', '1e400', '1e-400'].map(expires => ({ name: `numeric-expiry-${expires}`, steps: [{ fileEnv: '$TOKEN_FILE', file: `{"accessToken":"","refreshToken":"r","expiresAt":${expires}}` }] })),
  { name: 'text-fractional-and-expanded-year', steps: [{ fileEnv: '$TOKEN_FILE', file: '{"accessToken":"a","refreshToken":"r","expiresAt":0.99}', args: textArgs }, { clear: true, file: '{"accessToken":"a","refreshToken":"r","expiresAt":253402300800000}', args: textArgs }] },
  { name: 'empty-refresh-is-logged-out-with-identity-preserved', steps: [{ fileEnv: '$TOKEN_FILE', file: '{"accessToken":"a","refreshToken":"","expiresAt":1,"email":"","accountId":""}' }, { args: textArgs }] },
  { name: 'optional-desktop-fields-must-be-strings', steps: [{ fileEnv: '$TOKEN_FILE', file: '{"accessToken":"a","refreshToken":"r","expiresAt":1,"email":false,"accountId":7,"idToken":null,"clientId":[]}' }] },
  { name: 'unicode-identity-json-and-text', steps: [{ fileEnv: '$TOKEN_FILE', file: '{"accessToken":"a","refreshToken":"r","expiresAt":1,"email":"邮箱😀\\ud800\\u2028<>&","accountId":"\\udfff\\n\\t"}' }, { args: textArgs }] },
  { name: 'duplicate-keys-last-value-wins', steps: [{ fileEnv: '$TOKEN_FILE', file: '{"refreshToken":"wrong","accessToken":"a","refreshToken":"r","expiresAt":-1,"expiresAt":1,"email":"one","email":"two"}' }] },
  { name: 'raw-invalid-utf8-node-file-decoding', steps: [{ fileEnv: '$TOKEN_FILE', file: [...new TextEncoder().encode('{"accessToken":"a","refreshToken":"r","expiresAt":1,"email":"'), 0xe2, 0x82, 0x22, 0x7d] }] },
  { name: 'flags-repeat-and-parse-errors-stay-before-handler', steps: [{ secure: secure(), args: [...jsonArgs, '--openai', '--json', '--json'] }, { args: [...jsonArgs, '--unknown'] }, { args: [...jsonArgs, 'unexpected'] }, { args: [...jsonArgs, '--help'] }] },
  { name: 'source-only-raw-secure-store-shapes-and-date-errors', sourceOnly: true, steps: [{ secure: secure('{}') }, { clear: true, secure: secure('{"refreshToken":null,"expiresAt":1}') }, { clear: true, secure: secure('{"accessToken":"a","refreshToken":"r","expiresAt":1e400}'), args: textArgs }, { clear: true, secure: secure('{"accessToken":"a","refreshToken":"r","expiresAt":8640000000000001}'), args: textArgs }] },
]
