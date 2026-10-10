export type HeaderPair = [string, string]
export type HTTPMetadataCase = { name: string; kind: "constructed" | "http" | "sse" | "completion"; pairs: HeaderPair[]; record?: boolean; afterErrorOperations?: Array<{ kind: "append" | "set" | "delete"; name: string; value?: string }>; operations?: Array<{ kind: "append" | "set" | "delete"; name: string; value?: string }>; chunked?: boolean; connection?: string; version?: string; omitConnection?: boolean; eof?: boolean; latin1HeaderBytes?: boolean }
// Original inputs from pinned official classification and raw wire probes, never candidate output.
export const httpMetadataCases: HTTPMetadataCase[] = [
  {
    "name": "reverse-mixed",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "www-authenticate",
        "Bearer realm=\"fixture\""
      ],
      [
        "x-middle",
        "m"
      ],
      [
        "server",
        "server-fixture"
      ],
      [
        "content-type",
        "text/plain"
      ],
      [
        "date",
        "Thu, 01 Jan 1970 00:00:00 GMT"
      ],
      [
        "cache-control",
        "no-store"
      ],
      [
        "x-alpha",
        "a"
      ],
      [
        "request-id",
        "first-id"
      ],
      [
        "set-cookie",
        "a=1"
      ],
      [
        "set-cookie",
        "b=2"
      ],
      [
        "retry-after",
        "2"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "reverse-mixed-reversed",
    "pairs": [
      [
        "retry-after",
        "2"
      ],
      [
        "set-cookie",
        "b=2"
      ],
      [
        "set-cookie",
        "a=1"
      ],
      [
        "request-id",
        "first-id"
      ],
      [
        "x-alpha",
        "a"
      ],
      [
        "cache-control",
        "no-store"
      ],
      [
        "date",
        "Thu, 01 Jan 1970 00:00:00 GMT"
      ],
      [
        "content-type",
        "text/plain"
      ],
      [
        "server",
        "server-fixture"
      ],
      [
        "x-middle",
        "m"
      ],
      [
        "www-authenticate",
        "Bearer realm=\"fixture\""
      ],
      [
        "x-zulu",
        "z"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "unknown-order",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "x-alpha",
        "a"
      ],
      [
        "x-middle",
        "m"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "unknown-order-reversed",
    "pairs": [
      [
        "x-middle",
        "m"
      ],
      [
        "x-alpha",
        "a"
      ],
      [
        "x-zulu",
        "z"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "duplicate-case-empty",
    "pairs": [
      [
        "X-Zulu",
        "first"
      ],
      [
        "x-ZULU",
        "second"
      ],
      [
        "x-zulu",
        ""
      ],
      [
        "REQUEST-ID",
        "first-id"
      ],
      [
        "request-id",
        "last-id"
      ],
      [
        "Request-Id",
        ""
      ],
      [
        "content-type",
        "text/plain"
      ],
      [
        "CONTENT-TYPE",
        "application/json"
      ],
      [
        "Content-Type",
        ""
      ],
      [
        "set-cookie",
        "a=1"
      ],
      [
        "Set-Cookie",
        "b=2"
      ],
      [
        "SET-cookie",
        ""
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "empty-before-after",
    "pairs": [
      [
        "x-empty",
        ""
      ],
      [
        "x-space",
        " \t "
      ],
      [
        "x-zulu",
        ""
      ],
      [
        "x-zulu",
        "visible"
      ],
      [
        "x-alpha",
        "visible"
      ],
      [
        "x-alpha",
        ""
      ],
      [
        "request-id",
        ""
      ],
      [
        "request-id",
        "visible"
      ],
      [
        "request-id",
        ""
      ],
      [
        "cache-control",
        ""
      ],
      [
        "cache-control",
        "no-store"
      ],
      [
        "cache-control",
        ""
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "cookies-interleaved",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "set-cookie",
        "a=1"
      ],
      [
        "x-alpha",
        "a"
      ],
      [
        "Set-Cookie",
        "b=2"
      ],
      [
        "set-cookie",
        "c=3"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "known-subset",
    "pairs": [
      [
        "www-authenticate",
        "Basic fixture"
      ],
      [
        "vary",
        "accept"
      ],
      [
        "server",
        "fixture"
      ],
      [
        "retry-after",
        "3"
      ],
      [
        "location",
        "/fixture"
      ],
      [
        "last-modified",
        "Thu, 01 Jan 1970 00:00:00 GMT"
      ],
      [
        "etag",
        "\"fixture\""
      ],
      [
        "expires",
        "0"
      ],
      [
        "content-type",
        "text/plain"
      ],
      [
        "content-language",
        "en"
      ],
      [
        "content-encoding",
        "identity"
      ],
      [
        "cache-control",
        "no-cache"
      ],
      [
        "age",
        "1"
      ],
      [
        "accept-ranges",
        "bytes"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "known-subset-reversed",
    "pairs": [
      [
        "accept-ranges",
        "bytes"
      ],
      [
        "age",
        "1"
      ],
      [
        "cache-control",
        "no-cache"
      ],
      [
        "content-encoding",
        "identity"
      ],
      [
        "content-language",
        "en"
      ],
      [
        "content-type",
        "text/plain"
      ],
      [
        "expires",
        "0"
      ],
      [
        "etag",
        "\"fixture\""
      ],
      [
        "last-modified",
        "Thu, 01 Jan 1970 00:00:00 GMT"
      ],
      [
        "location",
        "/fixture"
      ],
      [
        "retry-after",
        "3"
      ],
      [
        "server",
        "fixture"
      ],
      [
        "vary",
        "accept"
      ],
      [
        "www-authenticate",
        "Basic fixture"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "empty-only",
    "pairs": [
      [
        "content-type",
        ""
      ],
      [
        "set-cookie",
        ""
      ],
      [
        "request-id",
        ""
      ],
      [
        "x-uncommon",
        ""
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "unknown-delete-reinsert",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "x-alpha",
        "a"
      ],
      [
        "x-middle",
        "m"
      ]
    ],
    "operations": [
      {
        "kind": "delete",
        "name": "x-alpha"
      },
      {
        "kind": "set",
        "name": "x-alpha",
        "value": "after"
      }
    ],
    "kind": "constructed"
  },
  {
    "name": "unknown-set-existing",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "x-alpha",
        "a"
      ],
      [
        "x-middle",
        "m"
      ]
    ],
    "operations": [
      {
        "kind": "set",
        "name": "x-zulu",
        "value": "after"
      }
    ],
    "kind": "constructed"
  },
  {
    "name": "complete-common-reverse",
    "pairs": [
      [
        "x-uncommon-start",
        "start"
      ],
      [
        "x-xss-protection",
        "fixture"
      ],
      [
        "x-temp-tablet",
        "fixture"
      ],
      [
        "x-sourcemap",
        "fixture"
      ],
      [
        "x-frame-options",
        "fixture"
      ],
      [
        "x-dns-prefetch-control",
        "fixture"
      ],
      [
        "x-content-type-options",
        "fixture"
      ],
      [
        "via",
        "fixture"
      ],
      [
        "vary",
        "fixture"
      ],
      [
        "user-agent",
        "fixture"
      ],
      [
        "upgrade-insecure-requests",
        "fixture"
      ],
      [
        "upgrade",
        "fixture"
      ],
      [
        "trailer",
        "fixture"
      ],
      [
        "timing-allow-origin",
        "fixture"
      ],
      [
        "te",
        "fixture"
      ],
      [
        "strict-transport-security",
        "fixture"
      ],
      [
        "sourcemap",
        "fixture"
      ],
      [
        "set-cookie2",
        "fixture"
      ],
      [
        "set-cookie",
        "fixture=1"
      ],
      [
        "service-worker-navigation-preload",
        "fixture"
      ],
      [
        "service-worker-allowed",
        "fixture"
      ],
      [
        "service-worker",
        "fixture"
      ],
      [
        "server-timing",
        "fixture"
      ],
      [
        "sec-websocket-version",
        "fixture"
      ],
      [
        "sec-websocket-protocol",
        "fixture"
      ],
      [
        "sec-websocket-key",
        "fixture"
      ],
      [
        "sec-websocket-extensions",
        "fixture"
      ],
      [
        "sec-websocket-accept",
        "fixture"
      ],
      [
        "sec-fetch-mode",
        "fixture"
      ],
      [
        "sec-fetch-dest",
        "fixture"
      ],
      [
        "report-to",
        "fixture"
      ],
      [
        "refresh",
        "fixture"
      ],
      [
        "referrer-policy",
        "fixture"
      ],
      [
        "referer",
        "fixture"
      ],
      [
        "range",
        "fixture"
      ],
      [
        "purpose",
        "fixture"
      ],
      [
        "proxy-connection",
        "fixture"
      ],
      [
        "proxy-authorization",
        "fixture"
      ],
      [
        "pragma",
        "fixture"
      ],
      [
        "ping-to",
        "fixture"
      ],
      [
        "ping-from",
        "fixture"
      ],
      [
        "origin",
        "fixture"
      ],
      [
        "location",
        "fixture"
      ],
      [
        "link",
        "fixture"
      ],
      [
        "last-modified",
        "fixture"
      ],
      [
        "last-event-id",
        "fixture"
      ],
      [
        "keep-alive",
        "fixture"
      ],
      [
        "if-unmodified-since",
        "fixture"
      ],
      [
        "if-range",
        "fixture"
      ],
      [
        "if-none-match",
        "fixture"
      ],
      [
        "if-modified-since",
        "fixture"
      ],
      [
        "if-match",
        "fixture"
      ],
      [
        "icy-metadata",
        "fixture"
      ],
      [
        "icy-metaint",
        "fixture"
      ],
      [
        "host",
        "fixture"
      ],
      [
        "expires",
        "fixture"
      ],
      [
        "expect",
        "fixture"
      ],
      [
        "etag",
        "fixture"
      ],
      [
        "default-style",
        "fixture"
      ],
      [
        "date",
        "fixture"
      ],
      [
        "dnt",
        "fixture"
      ],
      [
        "cross-origin-resource-policy",
        "fixture"
      ],
      [
        "cross-origin-opener-policy-report-only",
        "fixture"
      ],
      [
        "cross-origin-opener-policy",
        "fixture"
      ],
      [
        "cross-origin-embedder-policy-report-only",
        "fixture"
      ],
      [
        "cross-origin-embedder-policy",
        "fixture"
      ],
      [
        "cookie2",
        "fixture"
      ],
      [
        "cookie",
        "fixture"
      ],
      [
        "content-type",
        "fixture"
      ],
      [
        "content-security-policy-report-only",
        "fixture"
      ],
      [
        "content-security-policy",
        "fixture"
      ],
      [
        "content-range",
        "fixture"
      ],
      [
        "content-location",
        "fixture"
      ],
      [
        "content-length",
        "2"
      ],
      [
        "content-language",
        "fixture"
      ],
      [
        "content-encoding",
        "identity"
      ],
      [
        "content-disposition",
        "fixture"
      ],
      [
        "connection",
        "close"
      ],
      [
        "cache-control",
        "fixture"
      ],
      [
        "authorization",
        "fixture"
      ],
      [
        "age",
        "fixture"
      ],
      [
        "access-control-request-method",
        "fixture"
      ],
      [
        "access-control-request-headers",
        "fixture"
      ],
      [
        "access-control-max-age",
        "fixture"
      ],
      [
        "access-control-expose-headers",
        "fixture"
      ],
      [
        "access-control-allow-origin",
        "fixture"
      ],
      [
        "access-control-allow-methods",
        "fixture"
      ],
      [
        "access-control-allow-headers",
        "fixture"
      ],
      [
        "access-control-allow-credentials",
        "fixture"
      ],
      [
        "accept-ranges",
        "fixture"
      ],
      [
        "accept-language",
        "fixture"
      ],
      [
        "accept-encoding",
        "fixture"
      ],
      [
        "accept-charset",
        "fixture"
      ],
      [
        "accept",
        "fixture"
      ],
      [
        "x-uncommon-end",
        "end"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "complete-common-forward",
    "pairs": [
      [
        "x-uncommon-end",
        "end"
      ],
      [
        "accept",
        "fixture"
      ],
      [
        "accept-charset",
        "fixture"
      ],
      [
        "accept-encoding",
        "fixture"
      ],
      [
        "accept-language",
        "fixture"
      ],
      [
        "accept-ranges",
        "fixture"
      ],
      [
        "access-control-allow-credentials",
        "fixture"
      ],
      [
        "access-control-allow-headers",
        "fixture"
      ],
      [
        "access-control-allow-methods",
        "fixture"
      ],
      [
        "access-control-allow-origin",
        "fixture"
      ],
      [
        "access-control-expose-headers",
        "fixture"
      ],
      [
        "access-control-max-age",
        "fixture"
      ],
      [
        "access-control-request-headers",
        "fixture"
      ],
      [
        "access-control-request-method",
        "fixture"
      ],
      [
        "age",
        "fixture"
      ],
      [
        "authorization",
        "fixture"
      ],
      [
        "cache-control",
        "fixture"
      ],
      [
        "connection",
        "close"
      ],
      [
        "content-disposition",
        "fixture"
      ],
      [
        "content-encoding",
        "identity"
      ],
      [
        "content-language",
        "fixture"
      ],
      [
        "content-length",
        "2"
      ],
      [
        "content-location",
        "fixture"
      ],
      [
        "content-range",
        "fixture"
      ],
      [
        "content-security-policy",
        "fixture"
      ],
      [
        "content-security-policy-report-only",
        "fixture"
      ],
      [
        "content-type",
        "fixture"
      ],
      [
        "cookie",
        "fixture"
      ],
      [
        "cookie2",
        "fixture"
      ],
      [
        "cross-origin-embedder-policy",
        "fixture"
      ],
      [
        "cross-origin-embedder-policy-report-only",
        "fixture"
      ],
      [
        "cross-origin-opener-policy",
        "fixture"
      ],
      [
        "cross-origin-opener-policy-report-only",
        "fixture"
      ],
      [
        "cross-origin-resource-policy",
        "fixture"
      ],
      [
        "dnt",
        "fixture"
      ],
      [
        "date",
        "fixture"
      ],
      [
        "default-style",
        "fixture"
      ],
      [
        "etag",
        "fixture"
      ],
      [
        "expect",
        "fixture"
      ],
      [
        "expires",
        "fixture"
      ],
      [
        "host",
        "fixture"
      ],
      [
        "icy-metaint",
        "fixture"
      ],
      [
        "icy-metadata",
        "fixture"
      ],
      [
        "if-match",
        "fixture"
      ],
      [
        "if-modified-since",
        "fixture"
      ],
      [
        "if-none-match",
        "fixture"
      ],
      [
        "if-range",
        "fixture"
      ],
      [
        "if-unmodified-since",
        "fixture"
      ],
      [
        "keep-alive",
        "fixture"
      ],
      [
        "last-event-id",
        "fixture"
      ],
      [
        "last-modified",
        "fixture"
      ],
      [
        "link",
        "fixture"
      ],
      [
        "location",
        "fixture"
      ],
      [
        "origin",
        "fixture"
      ],
      [
        "ping-from",
        "fixture"
      ],
      [
        "ping-to",
        "fixture"
      ],
      [
        "pragma",
        "fixture"
      ],
      [
        "proxy-authorization",
        "fixture"
      ],
      [
        "proxy-connection",
        "fixture"
      ],
      [
        "purpose",
        "fixture"
      ],
      [
        "range",
        "fixture"
      ],
      [
        "referer",
        "fixture"
      ],
      [
        "referrer-policy",
        "fixture"
      ],
      [
        "refresh",
        "fixture"
      ],
      [
        "report-to",
        "fixture"
      ],
      [
        "sec-fetch-dest",
        "fixture"
      ],
      [
        "sec-fetch-mode",
        "fixture"
      ],
      [
        "sec-websocket-accept",
        "fixture"
      ],
      [
        "sec-websocket-extensions",
        "fixture"
      ],
      [
        "sec-websocket-key",
        "fixture"
      ],
      [
        "sec-websocket-protocol",
        "fixture"
      ],
      [
        "sec-websocket-version",
        "fixture"
      ],
      [
        "server-timing",
        "fixture"
      ],
      [
        "service-worker",
        "fixture"
      ],
      [
        "service-worker-allowed",
        "fixture"
      ],
      [
        "service-worker-navigation-preload",
        "fixture"
      ],
      [
        "set-cookie",
        "fixture=1"
      ],
      [
        "set-cookie2",
        "fixture"
      ],
      [
        "sourcemap",
        "fixture"
      ],
      [
        "strict-transport-security",
        "fixture"
      ],
      [
        "te",
        "fixture"
      ],
      [
        "timing-allow-origin",
        "fixture"
      ],
      [
        "trailer",
        "fixture"
      ],
      [
        "upgrade",
        "fixture"
      ],
      [
        "upgrade-insecure-requests",
        "fixture"
      ],
      [
        "user-agent",
        "fixture"
      ],
      [
        "vary",
        "fixture"
      ],
      [
        "via",
        "fixture"
      ],
      [
        "x-content-type-options",
        "fixture"
      ],
      [
        "x-dns-prefetch-control",
        "fixture"
      ],
      [
        "x-frame-options",
        "fixture"
      ],
      [
        "x-sourcemap",
        "fixture"
      ],
      [
        "x-temp-tablet",
        "fixture"
      ],
      [
        "x-xss-protection",
        "fixture"
      ],
      [
        "x-uncommon-start",
        "start"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "numeric-object-properties",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "10",
        "ten"
      ],
      [
        "2",
        "two"
      ],
      [
        "01",
        "leading"
      ],
      [
        "content-type",
        "text/plain"
      ],
      [
        "4294967295",
        "non-index"
      ],
      [
        "4294967294",
        "max-index"
      ],
      [
        "0",
        "zero"
      ],
      [
        "set-cookie",
        "a=1"
      ],
      [
        "constructor",
        "constructor"
      ],
      [
        "__proto__",
        "proto"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "cookie-delimiter",
    "pairs": [
      [
        "cookie",
        "a=1"
      ],
      [
        "Cookie",
        "b=2"
      ],
      [
        "cookie",
        ""
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "known-delete-reinsert",
    "pairs": [
      [
        "content-type",
        "text/plain"
      ],
      [
        "x-zulu",
        "z"
      ],
      [
        "date",
        "date-fixture"
      ]
    ],
    "operations": [
      {
        "kind": "delete",
        "name": "content-type"
      },
      {
        "kind": "set",
        "name": "content-type",
        "value": "application/json"
      }
    ],
    "kind": "constructed"
  },
  {
    "name": "object-constructor-profile",
    "record": true,
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "10",
        "ten"
      ],
      [
        "content-type",
        "text/plain"
      ],
      [
        "2",
        "two"
      ],
      [
        "x-alpha",
        "a"
      ],
      [
        "Content-Type",
        "application/json"
      ],
      [
        "set-cookie",
        "a=1"
      ],
      [
        "Set-Cookie",
        "b=2"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-transfer-encoding",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "transfer-encoding",
        "chunked"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-accept",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "accept",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-accept",
    "pairs": [
      [
        "accept",
        "fixture-first"
      ],
      [
        "ACCEPT",
        "fixture-last"
      ],
      [
        "accept",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-accept-charset",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "accept-charset",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-accept-charset",
    "pairs": [
      [
        "accept-charset",
        "fixture-first"
      ],
      [
        "ACCEPT-CHARSET",
        "fixture-last"
      ],
      [
        "accept-charset",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-accept-encoding",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "accept-encoding",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-accept-encoding",
    "pairs": [
      [
        "accept-encoding",
        "fixture-first"
      ],
      [
        "ACCEPT-ENCODING",
        "fixture-last"
      ],
      [
        "accept-encoding",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-accept-language",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "accept-language",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-accept-language",
    "pairs": [
      [
        "accept-language",
        "fixture-first"
      ],
      [
        "ACCEPT-LANGUAGE",
        "fixture-last"
      ],
      [
        "accept-language",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-accept-ranges",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "accept-ranges",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-accept-ranges",
    "pairs": [
      [
        "accept-ranges",
        "fixture-first"
      ],
      [
        "ACCEPT-RANGES",
        "fixture-last"
      ],
      [
        "accept-ranges",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-access-control-allow-credentials",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "access-control-allow-credentials",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-access-control-allow-credentials",
    "pairs": [
      [
        "access-control-allow-credentials",
        "fixture-first"
      ],
      [
        "ACCESS-CONTROL-ALLOW-CREDENTIALS",
        "fixture-last"
      ],
      [
        "access-control-allow-credentials",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-access-control-allow-headers",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "access-control-allow-headers",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-access-control-allow-headers",
    "pairs": [
      [
        "access-control-allow-headers",
        "fixture-first"
      ],
      [
        "ACCESS-CONTROL-ALLOW-HEADERS",
        "fixture-last"
      ],
      [
        "access-control-allow-headers",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-access-control-allow-methods",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "access-control-allow-methods",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-access-control-allow-methods",
    "pairs": [
      [
        "access-control-allow-methods",
        "fixture-first"
      ],
      [
        "ACCESS-CONTROL-ALLOW-METHODS",
        "fixture-last"
      ],
      [
        "access-control-allow-methods",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-access-control-allow-origin",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "access-control-allow-origin",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-access-control-allow-origin",
    "pairs": [
      [
        "access-control-allow-origin",
        "fixture-first"
      ],
      [
        "ACCESS-CONTROL-ALLOW-ORIGIN",
        "fixture-last"
      ],
      [
        "access-control-allow-origin",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-access-control-expose-headers",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "access-control-expose-headers",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-access-control-expose-headers",
    "pairs": [
      [
        "access-control-expose-headers",
        "fixture-first"
      ],
      [
        "ACCESS-CONTROL-EXPOSE-HEADERS",
        "fixture-last"
      ],
      [
        "access-control-expose-headers",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-access-control-max-age",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "access-control-max-age",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-access-control-max-age",
    "pairs": [
      [
        "access-control-max-age",
        "fixture-first"
      ],
      [
        "ACCESS-CONTROL-MAX-AGE",
        "fixture-last"
      ],
      [
        "access-control-max-age",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-access-control-request-headers",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "access-control-request-headers",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-access-control-request-headers",
    "pairs": [
      [
        "access-control-request-headers",
        "fixture-first"
      ],
      [
        "ACCESS-CONTROL-REQUEST-HEADERS",
        "fixture-last"
      ],
      [
        "access-control-request-headers",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-access-control-request-method",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "access-control-request-method",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-access-control-request-method",
    "pairs": [
      [
        "access-control-request-method",
        "fixture-first"
      ],
      [
        "ACCESS-CONTROL-REQUEST-METHOD",
        "fixture-last"
      ],
      [
        "access-control-request-method",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-age",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "age",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-age",
    "pairs": [
      [
        "age",
        "fixture-first"
      ],
      [
        "AGE",
        "fixture-last"
      ],
      [
        "age",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-authorization",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "authorization",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-authorization",
    "pairs": [
      [
        "authorization",
        "fixture-first"
      ],
      [
        "AUTHORIZATION",
        "fixture-last"
      ],
      [
        "authorization",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-cache-control",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "cache-control",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-cache-control",
    "pairs": [
      [
        "cache-control",
        "fixture-first"
      ],
      [
        "CACHE-CONTROL",
        "fixture-last"
      ],
      [
        "cache-control",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-connection",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "connection",
        "close"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-content-disposition",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "content-disposition",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-content-disposition",
    "pairs": [
      [
        "content-disposition",
        "fixture-first"
      ],
      [
        "CONTENT-DISPOSITION",
        "fixture-last"
      ],
      [
        "content-disposition",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-content-encoding",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "content-encoding",
        "identity"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-content-language",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "content-language",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-content-language",
    "pairs": [
      [
        "content-language",
        "fixture-first"
      ],
      [
        "CONTENT-LANGUAGE",
        "fixture-last"
      ],
      [
        "content-language",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-content-length",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "content-length",
        "2"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-content-location",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "content-location",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-content-location",
    "pairs": [
      [
        "content-location",
        "fixture-first"
      ],
      [
        "CONTENT-LOCATION",
        "fixture-last"
      ],
      [
        "content-location",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-content-range",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "content-range",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-content-range",
    "pairs": [
      [
        "content-range",
        "fixture-first"
      ],
      [
        "CONTENT-RANGE",
        "fixture-last"
      ],
      [
        "content-range",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-content-security-policy",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "content-security-policy",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-content-security-policy",
    "pairs": [
      [
        "content-security-policy",
        "fixture-first"
      ],
      [
        "CONTENT-SECURITY-POLICY",
        "fixture-last"
      ],
      [
        "content-security-policy",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-content-security-policy-report-only",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "content-security-policy-report-only",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-content-security-policy-report-only",
    "pairs": [
      [
        "content-security-policy-report-only",
        "fixture-first"
      ],
      [
        "CONTENT-SECURITY-POLICY-REPORT-ONLY",
        "fixture-last"
      ],
      [
        "content-security-policy-report-only",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-content-type",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "content-type",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-content-type",
    "pairs": [
      [
        "content-type",
        "fixture-first"
      ],
      [
        "CONTENT-TYPE",
        "fixture-last"
      ],
      [
        "content-type",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-cookie",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "cookie",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-cookie",
    "pairs": [
      [
        "cookie",
        "fixture-first"
      ],
      [
        "COOKIE",
        "fixture-last"
      ],
      [
        "cookie",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-cookie2",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "cookie2",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-cookie2",
    "pairs": [
      [
        "cookie2",
        "fixture-first"
      ],
      [
        "COOKIE2",
        "fixture-last"
      ],
      [
        "cookie2",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-cross-origin-embedder-policy",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "cross-origin-embedder-policy",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-cross-origin-embedder-policy",
    "pairs": [
      [
        "cross-origin-embedder-policy",
        "fixture-first"
      ],
      [
        "CROSS-ORIGIN-EMBEDDER-POLICY",
        "fixture-last"
      ],
      [
        "cross-origin-embedder-policy",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-cross-origin-embedder-policy-report-only",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "cross-origin-embedder-policy-report-only",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-cross-origin-embedder-policy-report-only",
    "pairs": [
      [
        "cross-origin-embedder-policy-report-only",
        "fixture-first"
      ],
      [
        "CROSS-ORIGIN-EMBEDDER-POLICY-REPORT-ONLY",
        "fixture-last"
      ],
      [
        "cross-origin-embedder-policy-report-only",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-cross-origin-opener-policy",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "cross-origin-opener-policy",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-cross-origin-opener-policy",
    "pairs": [
      [
        "cross-origin-opener-policy",
        "fixture-first"
      ],
      [
        "CROSS-ORIGIN-OPENER-POLICY",
        "fixture-last"
      ],
      [
        "cross-origin-opener-policy",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-cross-origin-opener-policy-report-only",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "cross-origin-opener-policy-report-only",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-cross-origin-opener-policy-report-only",
    "pairs": [
      [
        "cross-origin-opener-policy-report-only",
        "fixture-first"
      ],
      [
        "CROSS-ORIGIN-OPENER-POLICY-REPORT-ONLY",
        "fixture-last"
      ],
      [
        "cross-origin-opener-policy-report-only",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-cross-origin-resource-policy",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "cross-origin-resource-policy",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-cross-origin-resource-policy",
    "pairs": [
      [
        "cross-origin-resource-policy",
        "fixture-first"
      ],
      [
        "CROSS-ORIGIN-RESOURCE-POLICY",
        "fixture-last"
      ],
      [
        "cross-origin-resource-policy",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-dnt",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "dnt",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-dnt",
    "pairs": [
      [
        "dnt",
        "fixture-first"
      ],
      [
        "DNT",
        "fixture-last"
      ],
      [
        "dnt",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-date",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "date",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-date",
    "pairs": [
      [
        "date",
        "fixture-first"
      ],
      [
        "DATE",
        "fixture-last"
      ],
      [
        "date",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-default-style",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "default-style",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-default-style",
    "pairs": [
      [
        "default-style",
        "fixture-first"
      ],
      [
        "DEFAULT-STYLE",
        "fixture-last"
      ],
      [
        "default-style",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-etag",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "etag",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-etag",
    "pairs": [
      [
        "etag",
        "fixture-first"
      ],
      [
        "ETAG",
        "fixture-last"
      ],
      [
        "etag",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-expect",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "expect",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-expect",
    "pairs": [
      [
        "expect",
        "fixture-first"
      ],
      [
        "EXPECT",
        "fixture-last"
      ],
      [
        "expect",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-expires",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "expires",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-expires",
    "pairs": [
      [
        "expires",
        "fixture-first"
      ],
      [
        "EXPIRES",
        "fixture-last"
      ],
      [
        "expires",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-host",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "host",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-host",
    "pairs": [
      [
        "host",
        "fixture-first"
      ],
      [
        "HOST",
        "fixture-last"
      ],
      [
        "host",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-icy-metaint",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "icy-metaint",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-icy-metaint",
    "pairs": [
      [
        "icy-metaint",
        "fixture-first"
      ],
      [
        "ICY-METAINT",
        "fixture-last"
      ],
      [
        "icy-metaint",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-icy-metadata",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "icy-metadata",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-icy-metadata",
    "pairs": [
      [
        "icy-metadata",
        "fixture-first"
      ],
      [
        "ICY-METADATA",
        "fixture-last"
      ],
      [
        "icy-metadata",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-if-match",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "if-match",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-if-match",
    "pairs": [
      [
        "if-match",
        "fixture-first"
      ],
      [
        "IF-MATCH",
        "fixture-last"
      ],
      [
        "if-match",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-if-modified-since",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "if-modified-since",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-if-modified-since",
    "pairs": [
      [
        "if-modified-since",
        "fixture-first"
      ],
      [
        "IF-MODIFIED-SINCE",
        "fixture-last"
      ],
      [
        "if-modified-since",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-if-none-match",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "if-none-match",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-if-none-match",
    "pairs": [
      [
        "if-none-match",
        "fixture-first"
      ],
      [
        "IF-NONE-MATCH",
        "fixture-last"
      ],
      [
        "if-none-match",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-if-range",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "if-range",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-if-range",
    "pairs": [
      [
        "if-range",
        "fixture-first"
      ],
      [
        "IF-RANGE",
        "fixture-last"
      ],
      [
        "if-range",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-if-unmodified-since",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "if-unmodified-since",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-if-unmodified-since",
    "pairs": [
      [
        "if-unmodified-since",
        "fixture-first"
      ],
      [
        "IF-UNMODIFIED-SINCE",
        "fixture-last"
      ],
      [
        "if-unmodified-since",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-keep-alive",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "keep-alive",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-keep-alive",
    "pairs": [
      [
        "keep-alive",
        "fixture-first"
      ],
      [
        "KEEP-ALIVE",
        "fixture-last"
      ],
      [
        "keep-alive",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-last-event-id",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "last-event-id",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-last-event-id",
    "pairs": [
      [
        "last-event-id",
        "fixture-first"
      ],
      [
        "LAST-EVENT-ID",
        "fixture-last"
      ],
      [
        "last-event-id",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-last-modified",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "last-modified",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-last-modified",
    "pairs": [
      [
        "last-modified",
        "fixture-first"
      ],
      [
        "LAST-MODIFIED",
        "fixture-last"
      ],
      [
        "last-modified",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-link",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "link",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-link",
    "pairs": [
      [
        "link",
        "fixture-first"
      ],
      [
        "LINK",
        "fixture-last"
      ],
      [
        "link",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-location",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "location",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-location",
    "pairs": [
      [
        "location",
        "fixture-first"
      ],
      [
        "LOCATION",
        "fixture-last"
      ],
      [
        "location",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-origin",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "origin",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-origin",
    "pairs": [
      [
        "origin",
        "fixture-first"
      ],
      [
        "ORIGIN",
        "fixture-last"
      ],
      [
        "origin",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-ping-from",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "ping-from",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-ping-from",
    "pairs": [
      [
        "ping-from",
        "fixture-first"
      ],
      [
        "PING-FROM",
        "fixture-last"
      ],
      [
        "ping-from",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-ping-to",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "ping-to",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-ping-to",
    "pairs": [
      [
        "ping-to",
        "fixture-first"
      ],
      [
        "PING-TO",
        "fixture-last"
      ],
      [
        "ping-to",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-pragma",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "pragma",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-pragma",
    "pairs": [
      [
        "pragma",
        "fixture-first"
      ],
      [
        "PRAGMA",
        "fixture-last"
      ],
      [
        "pragma",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-proxy-authorization",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "proxy-authorization",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-proxy-authorization",
    "pairs": [
      [
        "proxy-authorization",
        "fixture-first"
      ],
      [
        "PROXY-AUTHORIZATION",
        "fixture-last"
      ],
      [
        "proxy-authorization",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-proxy-connection",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "proxy-connection",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-proxy-connection",
    "pairs": [
      [
        "proxy-connection",
        "fixture-first"
      ],
      [
        "PROXY-CONNECTION",
        "fixture-last"
      ],
      [
        "proxy-connection",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-purpose",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "purpose",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-purpose",
    "pairs": [
      [
        "purpose",
        "fixture-first"
      ],
      [
        "PURPOSE",
        "fixture-last"
      ],
      [
        "purpose",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-range",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "range",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-range",
    "pairs": [
      [
        "range",
        "fixture-first"
      ],
      [
        "RANGE",
        "fixture-last"
      ],
      [
        "range",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-referer",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "referer",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-referer",
    "pairs": [
      [
        "referer",
        "fixture-first"
      ],
      [
        "REFERER",
        "fixture-last"
      ],
      [
        "referer",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-referrer-policy",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "referrer-policy",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-referrer-policy",
    "pairs": [
      [
        "referrer-policy",
        "fixture-first"
      ],
      [
        "REFERRER-POLICY",
        "fixture-last"
      ],
      [
        "referrer-policy",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-refresh",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "refresh",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-refresh",
    "pairs": [
      [
        "refresh",
        "fixture-first"
      ],
      [
        "REFRESH",
        "fixture-last"
      ],
      [
        "refresh",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-report-to",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "report-to",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-report-to",
    "pairs": [
      [
        "report-to",
        "fixture-first"
      ],
      [
        "REPORT-TO",
        "fixture-last"
      ],
      [
        "report-to",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-sec-fetch-dest",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "sec-fetch-dest",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-sec-fetch-dest",
    "pairs": [
      [
        "sec-fetch-dest",
        "fixture-first"
      ],
      [
        "SEC-FETCH-DEST",
        "fixture-last"
      ],
      [
        "sec-fetch-dest",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-sec-fetch-mode",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "sec-fetch-mode",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-sec-fetch-mode",
    "pairs": [
      [
        "sec-fetch-mode",
        "fixture-first"
      ],
      [
        "SEC-FETCH-MODE",
        "fixture-last"
      ],
      [
        "sec-fetch-mode",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-sec-websocket-accept",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "sec-websocket-accept",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-sec-websocket-accept",
    "pairs": [
      [
        "sec-websocket-accept",
        "fixture-first"
      ],
      [
        "SEC-WEBSOCKET-ACCEPT",
        "fixture-last"
      ],
      [
        "sec-websocket-accept",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-sec-websocket-extensions",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "sec-websocket-extensions",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-sec-websocket-extensions",
    "pairs": [
      [
        "sec-websocket-extensions",
        "fixture-first"
      ],
      [
        "SEC-WEBSOCKET-EXTENSIONS",
        "fixture-last"
      ],
      [
        "sec-websocket-extensions",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-sec-websocket-key",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "sec-websocket-key",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-sec-websocket-key",
    "pairs": [
      [
        "sec-websocket-key",
        "fixture-first"
      ],
      [
        "SEC-WEBSOCKET-KEY",
        "fixture-last"
      ],
      [
        "sec-websocket-key",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-sec-websocket-protocol",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "sec-websocket-protocol",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-sec-websocket-protocol",
    "pairs": [
      [
        "sec-websocket-protocol",
        "fixture-first"
      ],
      [
        "SEC-WEBSOCKET-PROTOCOL",
        "fixture-last"
      ],
      [
        "sec-websocket-protocol",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-sec-websocket-version",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "sec-websocket-version",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-sec-websocket-version",
    "pairs": [
      [
        "sec-websocket-version",
        "fixture-first"
      ],
      [
        "SEC-WEBSOCKET-VERSION",
        "fixture-last"
      ],
      [
        "sec-websocket-version",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-server-timing",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "server-timing",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-server-timing",
    "pairs": [
      [
        "server-timing",
        "fixture-first"
      ],
      [
        "SERVER-TIMING",
        "fixture-last"
      ],
      [
        "server-timing",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-service-worker",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "service-worker",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-service-worker",
    "pairs": [
      [
        "service-worker",
        "fixture-first"
      ],
      [
        "SERVICE-WORKER",
        "fixture-last"
      ],
      [
        "service-worker",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-service-worker-allowed",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "service-worker-allowed",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-service-worker-allowed",
    "pairs": [
      [
        "service-worker-allowed",
        "fixture-first"
      ],
      [
        "SERVICE-WORKER-ALLOWED",
        "fixture-last"
      ],
      [
        "service-worker-allowed",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-service-worker-navigation-preload",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "service-worker-navigation-preload",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-service-worker-navigation-preload",
    "pairs": [
      [
        "service-worker-navigation-preload",
        "fixture-first"
      ],
      [
        "SERVICE-WORKER-NAVIGATION-PRELOAD",
        "fixture-last"
      ],
      [
        "service-worker-navigation-preload",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-set-cookie",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "set-cookie",
        "fixture=1"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-set-cookie",
    "pairs": [
      [
        "set-cookie",
        "fixture-first=1"
      ],
      [
        "SET-COOKIE",
        "fixture-last=1"
      ],
      [
        "set-cookie",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-set-cookie2",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "set-cookie2",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-set-cookie2",
    "pairs": [
      [
        "set-cookie2",
        "fixture-first"
      ],
      [
        "SET-COOKIE2",
        "fixture-last"
      ],
      [
        "set-cookie2",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-sourcemap",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "sourcemap",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-sourcemap",
    "pairs": [
      [
        "sourcemap",
        "fixture-first"
      ],
      [
        "SOURCEMAP",
        "fixture-last"
      ],
      [
        "sourcemap",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-strict-transport-security",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "strict-transport-security",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-strict-transport-security",
    "pairs": [
      [
        "strict-transport-security",
        "fixture-first"
      ],
      [
        "STRICT-TRANSPORT-SECURITY",
        "fixture-last"
      ],
      [
        "strict-transport-security",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-te",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "te",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-te",
    "pairs": [
      [
        "te",
        "fixture-first"
      ],
      [
        "TE",
        "fixture-last"
      ],
      [
        "te",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-timing-allow-origin",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "timing-allow-origin",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-timing-allow-origin",
    "pairs": [
      [
        "timing-allow-origin",
        "fixture-first"
      ],
      [
        "TIMING-ALLOW-ORIGIN",
        "fixture-last"
      ],
      [
        "timing-allow-origin",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-trailer",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "trailer",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-trailer",
    "pairs": [
      [
        "trailer",
        "fixture-first"
      ],
      [
        "TRAILER",
        "fixture-last"
      ],
      [
        "trailer",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-upgrade",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "upgrade",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-upgrade",
    "pairs": [
      [
        "upgrade",
        "fixture-first"
      ],
      [
        "UPGRADE",
        "fixture-last"
      ],
      [
        "upgrade",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-upgrade-insecure-requests",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "upgrade-insecure-requests",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-upgrade-insecure-requests",
    "pairs": [
      [
        "upgrade-insecure-requests",
        "fixture-first"
      ],
      [
        "UPGRADE-INSECURE-REQUESTS",
        "fixture-last"
      ],
      [
        "upgrade-insecure-requests",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-user-agent",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "user-agent",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-user-agent",
    "pairs": [
      [
        "user-agent",
        "fixture-first"
      ],
      [
        "USER-AGENT",
        "fixture-last"
      ],
      [
        "user-agent",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-vary",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "vary",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-vary",
    "pairs": [
      [
        "vary",
        "fixture-first"
      ],
      [
        "VARY",
        "fixture-last"
      ],
      [
        "vary",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-via",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "via",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-via",
    "pairs": [
      [
        "via",
        "fixture-first"
      ],
      [
        "VIA",
        "fixture-last"
      ],
      [
        "via",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-x-content-type-options",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "x-content-type-options",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-x-content-type-options",
    "pairs": [
      [
        "x-content-type-options",
        "fixture-first"
      ],
      [
        "X-CONTENT-TYPE-OPTIONS",
        "fixture-last"
      ],
      [
        "x-content-type-options",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-x-dns-prefetch-control",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "x-dns-prefetch-control",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-x-dns-prefetch-control",
    "pairs": [
      [
        "x-dns-prefetch-control",
        "fixture-first"
      ],
      [
        "X-DNS-PREFETCH-CONTROL",
        "fixture-last"
      ],
      [
        "x-dns-prefetch-control",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-x-frame-options",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "x-frame-options",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-x-frame-options",
    "pairs": [
      [
        "x-frame-options",
        "fixture-first"
      ],
      [
        "X-FRAME-OPTIONS",
        "fixture-last"
      ],
      [
        "x-frame-options",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-x-sourcemap",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "x-sourcemap",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-x-sourcemap",
    "pairs": [
      [
        "x-sourcemap",
        "fixture-first"
      ],
      [
        "X-SOURCEMAP",
        "fixture-last"
      ],
      [
        "x-sourcemap",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-x-temp-tablet",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "x-temp-tablet",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-x-temp-tablet",
    "pairs": [
      [
        "x-temp-tablet",
        "fixture-first"
      ],
      [
        "X-TEMP-TABLET",
        "fixture-last"
      ],
      [
        "x-temp-tablet",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-classification-x-xss-protection",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "x-xss-protection",
        "fixture"
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "all-common-duplicate-x-xss-protection",
    "pairs": [
      [
        "x-xss-protection",
        "fixture-first"
      ],
      [
        "X-XSS-PROTECTION",
        "fixture-last"
      ],
      [
        "x-xss-protection",
        " \t "
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "uncommon-duplicate-server",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "server",
        "first"
      ],
      [
        "SERVER",
        "last"
      ],
      [
        "server",
        ""
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "uncommon-duplicate-retry-after",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "retry-after",
        "first"
      ],
      [
        "RETRY-AFTER",
        "last"
      ],
      [
        "retry-after",
        ""
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "uncommon-duplicate-www-authenticate",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "www-authenticate",
        "first"
      ],
      [
        "WWW-AUTHENTICATE",
        "last"
      ],
      [
        "www-authenticate",
        ""
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "uncommon-duplicate-request-id",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "request-id",
        "first"
      ],
      [
        "REQUEST-ID",
        "last"
      ],
      [
        "request-id",
        ""
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "uncommon-duplicate-x-future",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "x-future",
        "first"
      ],
      [
        "X-FUTURE",
        "last"
      ],
      [
        "x-future",
        ""
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "uncommon-duplicate-anthropic-ratelimit-requests-limit",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "anthropic-ratelimit-requests-limit",
        "first"
      ],
      [
        "ANTHROPIC-RATELIMIT-REQUESTS-LIMIT",
        "last"
      ],
      [
        "anthropic-ratelimit-requests-limit",
        ""
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "uncommon-duplicate-anthropic-ratelimit-tokens-limit",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "anthropic-ratelimit-tokens-limit",
        "first"
      ],
      [
        "ANTHROPIC-RATELIMIT-TOKENS-LIMIT",
        "last"
      ],
      [
        "anthropic-ratelimit-tokens-limit",
        ""
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "uncommon-duplicate-x-random-unknown",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "x-random-unknown",
        "first"
      ],
      [
        "X-RANDOM-UNKNOWN",
        "last"
      ],
      [
        "x-random-unknown",
        ""
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "uncommon-duplicate-999",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "999",
        "first"
      ],
      [
        "999",
        "last"
      ],
      [
        "999",
        ""
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "uncommon-duplicate-x-content-type-options-suffix",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "x-content-type-options-suffix",
        "first"
      ],
      [
        "X-CONTENT-TYPE-OPTIONS-SUFFIX",
        "last"
      ],
      [
        "x-content-type-options-suffix",
        ""
      ],
      [
        "x-alpha",
        "a"
      ]
    ],
    "kind": "constructed"
  },
  {
    "name": "http-close-reverse-mixed",
    "kind": "http",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "www-authenticate",
        "Bearer realm=\"fixture\""
      ],
      [
        "x-middle",
        "m"
      ],
      [
        "server",
        "server-fixture"
      ],
      [
        "content-type",
        "text/plain"
      ],
      [
        "date",
        "Thu, 01 Jan 1970 00:00:00 GMT"
      ],
      [
        "cache-control",
        "no-store"
      ],
      [
        "x-alpha",
        "a"
      ],
      [
        "request-id",
        "first-id"
      ],
      [
        "set-cookie",
        "a=1"
      ],
      [
        "set-cookie",
        "b=2"
      ],
      [
        "retry-after",
        "2"
      ]
    ]
  },
  {
    "name": "sse-close-reverse-mixed",
    "kind": "sse",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "www-authenticate",
        "Bearer realm=\"fixture\""
      ],
      [
        "x-middle",
        "m"
      ],
      [
        "server",
        "server-fixture"
      ],
      [
        "content-type",
        "text/plain"
      ],
      [
        "date",
        "Thu, 01 Jan 1970 00:00:00 GMT"
      ],
      [
        "cache-control",
        "no-store"
      ],
      [
        "x-alpha",
        "a"
      ],
      [
        "request-id",
        "first-id"
      ],
      [
        "set-cookie",
        "a=1"
      ],
      [
        "set-cookie",
        "b=2"
      ],
      [
        "retry-after",
        "2"
      ]
    ]
  },
  {
    "name": "completion-close-reverse-mixed",
    "kind": "completion",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "www-authenticate",
        "Bearer realm=\"fixture\""
      ],
      [
        "x-middle",
        "m"
      ],
      [
        "server",
        "server-fixture"
      ],
      [
        "content-type",
        "text/plain"
      ],
      [
        "date",
        "Thu, 01 Jan 1970 00:00:00 GMT"
      ],
      [
        "cache-control",
        "no-store"
      ],
      [
        "x-alpha",
        "a"
      ],
      [
        "request-id",
        "first-id"
      ],
      [
        "set-cookie",
        "a=1"
      ],
      [
        "set-cookie",
        "b=2"
      ],
      [
        "retry-after",
        "2"
      ]
    ]
  },
  {
    "name": "http-close-reverse-mixed-reversed",
    "kind": "http",
    "pairs": [
      [
        "retry-after",
        "2"
      ],
      [
        "set-cookie",
        "b=2"
      ],
      [
        "set-cookie",
        "a=1"
      ],
      [
        "request-id",
        "first-id"
      ],
      [
        "x-alpha",
        "a"
      ],
      [
        "cache-control",
        "no-store"
      ],
      [
        "date",
        "Thu, 01 Jan 1970 00:00:00 GMT"
      ],
      [
        "content-type",
        "text/plain"
      ],
      [
        "server",
        "server-fixture"
      ],
      [
        "x-middle",
        "m"
      ],
      [
        "www-authenticate",
        "Bearer realm=\"fixture\""
      ],
      [
        "x-zulu",
        "z"
      ]
    ]
  },
  {
    "name": "sse-close-reverse-mixed-reversed",
    "kind": "sse",
    "pairs": [
      [
        "retry-after",
        "2"
      ],
      [
        "set-cookie",
        "b=2"
      ],
      [
        "set-cookie",
        "a=1"
      ],
      [
        "request-id",
        "first-id"
      ],
      [
        "x-alpha",
        "a"
      ],
      [
        "cache-control",
        "no-store"
      ],
      [
        "date",
        "Thu, 01 Jan 1970 00:00:00 GMT"
      ],
      [
        "content-type",
        "text/plain"
      ],
      [
        "server",
        "server-fixture"
      ],
      [
        "x-middle",
        "m"
      ],
      [
        "www-authenticate",
        "Bearer realm=\"fixture\""
      ],
      [
        "x-zulu",
        "z"
      ]
    ]
  },
  {
    "name": "completion-close-reverse-mixed-reversed",
    "kind": "completion",
    "pairs": [
      [
        "retry-after",
        "2"
      ],
      [
        "set-cookie",
        "b=2"
      ],
      [
        "set-cookie",
        "a=1"
      ],
      [
        "request-id",
        "first-id"
      ],
      [
        "x-alpha",
        "a"
      ],
      [
        "cache-control",
        "no-store"
      ],
      [
        "date",
        "Thu, 01 Jan 1970 00:00:00 GMT"
      ],
      [
        "content-type",
        "text/plain"
      ],
      [
        "server",
        "server-fixture"
      ],
      [
        "x-middle",
        "m"
      ],
      [
        "www-authenticate",
        "Bearer realm=\"fixture\""
      ],
      [
        "x-zulu",
        "z"
      ]
    ]
  },
  {
    "name": "http-close-duplicates-empty",
    "kind": "http",
    "pairs": [
      [
        "X-Zulu",
        "first"
      ],
      [
        "x-ZULU",
        "second"
      ],
      [
        "x-zulu",
        ""
      ],
      [
        "Request-ID",
        "first"
      ],
      [
        "request-id",
        "second"
      ],
      [
        "Request-Id",
        ""
      ],
      [
        "content-type",
        "application/json"
      ],
      [
        "CONTENT-TYPE",
        "text/plain"
      ],
      [
        "set-cookie",
        "a=1"
      ],
      [
        "Set-Cookie",
        "b=2"
      ]
    ]
  },
  {
    "name": "sse-close-duplicates-empty",
    "kind": "sse",
    "pairs": [
      [
        "X-Zulu",
        "first"
      ],
      [
        "x-ZULU",
        "second"
      ],
      [
        "x-zulu",
        ""
      ],
      [
        "Request-ID",
        "first"
      ],
      [
        "request-id",
        "second"
      ],
      [
        "Request-Id",
        ""
      ],
      [
        "content-type",
        "application/json"
      ],
      [
        "CONTENT-TYPE",
        "text/plain"
      ],
      [
        "set-cookie",
        "a=1"
      ],
      [
        "Set-Cookie",
        "b=2"
      ]
    ]
  },
  {
    "name": "completion-close-duplicates-empty",
    "kind": "completion",
    "pairs": [
      [
        "X-Zulu",
        "first"
      ],
      [
        "x-ZULU",
        "second"
      ],
      [
        "x-zulu",
        ""
      ],
      [
        "Request-ID",
        "first"
      ],
      [
        "request-id",
        "second"
      ],
      [
        "Request-Id",
        ""
      ],
      [
        "content-type",
        "application/json"
      ],
      [
        "CONTENT-TYPE",
        "text/plain"
      ],
      [
        "set-cookie",
        "a=1"
      ],
      [
        "Set-Cookie",
        "b=2"
      ]
    ]
  },
  {
    "name": "http-close-numeric",
    "kind": "http",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "10",
        "ten"
      ],
      [
        "2",
        "two"
      ],
      [
        "content-type",
        "application/json"
      ],
      [
        "set-cookie",
        "a=1"
      ],
      [
        "x-alpha",
        "a"
      ]
    ]
  },
  {
    "name": "sse-close-numeric",
    "kind": "sse",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "10",
        "ten"
      ],
      [
        "2",
        "two"
      ],
      [
        "content-type",
        "application/json"
      ],
      [
        "set-cookie",
        "a=1"
      ],
      [
        "x-alpha",
        "a"
      ]
    ]
  },
  {
    "name": "completion-close-numeric",
    "kind": "completion",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "10",
        "ten"
      ],
      [
        "2",
        "two"
      ],
      [
        "content-type",
        "application/json"
      ],
      [
        "set-cookie",
        "a=1"
      ],
      [
        "x-alpha",
        "a"
      ]
    ]
  },
  {
    "name": "http-close-common-uncommon",
    "kind": "http",
    "pairs": [
      [
        "server",
        "first"
      ],
      [
        "Server",
        "last"
      ],
      [
        "cache-control",
        "first"
      ],
      [
        "CACHE-CONTROL",
        "last"
      ],
      [
        "cookie",
        "a=1"
      ],
      [
        "Cookie",
        "b=2"
      ]
    ]
  },
  {
    "name": "sse-close-common-uncommon",
    "kind": "sse",
    "pairs": [
      [
        "server",
        "first"
      ],
      [
        "Server",
        "last"
      ],
      [
        "cache-control",
        "first"
      ],
      [
        "CACHE-CONTROL",
        "last"
      ],
      [
        "cookie",
        "a=1"
      ],
      [
        "Cookie",
        "b=2"
      ]
    ]
  },
  {
    "name": "completion-close-common-uncommon",
    "kind": "completion",
    "pairs": [
      [
        "server",
        "first"
      ],
      [
        "Server",
        "last"
      ],
      [
        "cache-control",
        "first"
      ],
      [
        "CACHE-CONTROL",
        "last"
      ],
      [
        "cookie",
        "a=1"
      ],
      [
        "Cookie",
        "b=2"
      ]
    ]
  },
  {
    "name": "http-chunked-close",
    "kind": "http",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "request-id",
        "request"
      ],
      [
        "set-cookie",
        "a=1"
      ],
      [
        "cache-control",
        "no-store"
      ]
    ],
    "chunked": true
  },
  {
    "name": "http-mixed-close",
    "kind": "http",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "request-id",
        "request"
      ]
    ],
    "connection": "keep-alive, CLOSE"
  },
  {
    "name": "http-http10-without-connection",
    "kind": "http",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "request-id",
        "request"
      ]
    ],
    "version": "HTTP/1.0",
    "omitConnection": true
  },
  {
    "name": "http-eof-without-framing",
    "kind": "http",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "request-id",
        "request"
      ]
    ],
    "omitConnection": true,
    "eof": true
  },
  {
    "name": "sse-chunked-close",
    "kind": "sse",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "request-id",
        "request"
      ],
      [
        "set-cookie",
        "a=1"
      ],
      [
        "cache-control",
        "no-store"
      ]
    ],
    "chunked": true
  },
  {
    "name": "sse-mixed-close",
    "kind": "sse",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "request-id",
        "request"
      ]
    ],
    "connection": "keep-alive, CLOSE"
  },
  {
    "name": "sse-http10-without-connection",
    "kind": "sse",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "request-id",
        "request"
      ]
    ],
    "version": "HTTP/1.0",
    "omitConnection": true
  },
  {
    "name": "sse-eof-without-framing",
    "kind": "sse",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "request-id",
        "request"
      ]
    ],
    "omitConnection": true,
    "eof": true
  },
  {
    "name": "completion-chunked-close",
    "kind": "completion",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "request-id",
        "request"
      ],
      [
        "set-cookie",
        "a=1"
      ],
      [
        "cache-control",
        "no-store"
      ]
    ],
    "chunked": true
  },
  {
    "name": "completion-mixed-close",
    "kind": "completion",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "request-id",
        "request"
      ]
    ],
    "connection": "keep-alive, CLOSE"
  },
  {
    "name": "completion-http10-without-connection",
    "kind": "completion",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "request-id",
        "request"
      ]
    ],
    "version": "HTTP/1.0",
    "omitConnection": true
  },
  {
    "name": "completion-eof-without-framing",
    "kind": "completion",
    "pairs": [
      [
        "x-zulu",
        "z"
      ],
      [
        "request-id",
        "request"
      ]
    ],
    "omitConnection": true,
    "eof": true
  }
]

// APIError retains its Headers object; RequestID remains a construction-time value.
for (const kind of ['constructed', 'http', 'sse', 'completion'] as const) {
  httpMetadataCases.push({
    name: `${kind}-post-error-shared-headers`, kind,
    pairs: [['request-id', 'original-id'], ['content-type', 'application/json'], ['retry-after', '9'], ['set-cookie', 'a=1'], ['x-last', 'tail']],
    afterErrorOperations: [
      { kind: 'set', name: 'request-id', value: 'mutated-id' },
      { kind: 'set', name: 'retry-after', value: '19' },
      { kind: 'delete', name: 'content-type' },
      { kind: 'append', name: 'content-type', value: 'new/type' },
      { kind: 'append', name: 'set-cookie', value: 'b=2' },
    ],
  })
}

// The raw socket carries these original bytes, independent of candidate decoding.
for (const kind of ['http', 'sse', 'completion'] as const) {
  for (const latin1HeaderBytes of [false, true]) {
    httpMetadataCases.push({ name: `${kind}-wire-${latin1HeaderBytes ? 'latin1' : 'utf8'}`, kind,
      pairs: [['request-id', 'byte-id'], ['x-raw', 'éÿ'], ['content-type', 'text/event-stream'], ['set-cookie', 'é=ÿ']], latin1HeaderBytes })
  }
}
