# Security policy

## Reporting a vulnerability

Report vulnerabilities privately through GitHub's private vulnerability reporting: open the repository's **Security** tab and choose **Report a vulnerability**, or go straight to
<https://github.com/SkilledAlpaca/printpaca/security/advisories/new>.
Include the version or commit, the affected surface, and how to reproduce the issue.

For reports that are not sensitive (a hardening suggestion, a finding that is already public), a regular issue is fine. Issues are public, so don't paste working exploit code or real customer data into one.

printpaca handles client contact details, invoices, and payment records, so security reports are triaged ahead of other issues. Expect an initial response within a few days.

## Supported versions

printpaca ships tagged releases. The Docker Hub image `skilledalpaca/printpaca:latest` tracks the newest stable tag, and `main` is the development line. Fixes ship as a new tag; there are no separate maintenance branches. Report vulnerabilities against the latest tag or against `main`.

## Scope

In scope:

- The main port (`PORT`): the admin app and API under `/admin/` (authentication, session handling, CSRF and `Host` allowlist enforcement, Cloudflare Access verification, and the tunnel guard), and the customer pages and `/api/public/*` served beside it.
- The optional customer-only listener (`PUBLIC_PORT`).
- Anything reachable without authentication, including the server-side price recompute.
- The Spoolman and Moonraker integration proxies.
- The build and the shipped image (supply chain, container hardening).

Out of scope:

- Behavior with authentication explicitly disabled (`ADMIN_PASSWORD=`), which is a documented, loudly-warned opt-out.
- Your own reverse proxy, TLS, or network configuration in front of printpaca (see [Configuration](docs/configuration.md)'s [Running behind a reverse proxy (HTTPS)](docs/configuration.md#running-behind-a-reverse-proxy-https) section for the supported setup).
- Findings that require an already-compromised host or config directory.
