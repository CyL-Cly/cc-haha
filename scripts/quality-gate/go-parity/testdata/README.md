# Loopback TLS fixture

`private-loopback-certificate.pem` and `private-loopback-key.pem` are a public,
synthetic test pair for the deterministic Go/Source TLS consumers. They contain
no account credentials. The SANs cover `localhost`, `127.0.0.1`, and `::1`; each
consumer installs this certificate only in its private test trust configuration.
Its fixed 2020–2120 validity supports fixture clocks without relying on the date
on which a checkout or certificate was created.

Keep the same bytes on every checkout. The tests compare physical TLS responses
and include these files in the repository input snapshot. The exact two fixture
paths are exempted from the general `.pem` ignore rule so a fresh checkout has
the required inputs.
