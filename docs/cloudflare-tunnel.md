# Cloudflare Tunnel

This guide sets up an optional [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/) in front of printpaca, so customers and your staff can reach it on one real hostname without port forwarding on your router. Cloudflare is optional and recommended: printpaca also runs on a LAN, or behind your own reverse proxy (see [Running behind a reverse proxy](configuration.md#running-behind-a-reverse-proxy-https)).

## What you get

One hostname, for example `printpaca.example.com`, that points at printpaca's main port (`PORT`, default `8080`):

- `/` is the customer front door, and `/order` is the shop with the [item gallery](configuration.md#items-gallery) and estimate form.
- `/admin/` is the admin app and its API.

Cloudflare terminates TLS and proxies back to your container over an outbound-only tunnel: no inbound port forwarding, and no `ports:` entry for the tunnel. The admin app lives entirely under `/admin/`, so a single Cloudflare Access application scoped to the path `/admin` protects it and leaves every customer path open. See [Routing and hostnames](configuration.md#routing-and-hostnames) for the full path table.

## Prerequisites

- A Cloudflare account with a zone (a domain managed by Cloudflare).
- printpaca running through `docker-compose.yml`.

## Creating the tunnel

1. In the Cloudflare dashboard, go to **Zero Trust > Networks > Tunnels**.
2. Select **Create a tunnel**, then choose the **Cloudflared** connector type.
3. Name the tunnel (for example `printpaca`) and continue.
4. Copy the tunnel token from the install command Cloudflare shows: the long string after `--token`. Don't run that command; the `cloudflared` service in `docker-compose.yml` runs the connector.

## Pointing the hostname at printpaca

Still in the tunnel's setup flow, add a **Public hostname**:

- **Subdomain and domain**: the hostname you want people to use, for example `printpaca` on `example.com` for `printpaca.example.com`.
- **Service type**: `HTTP`.
- **URL**: `printpaca:<PORT>`, using your `PORT` value. With the default, the URL is `printpaca:8080`.

Warning: printpaca sends `Strict-Transport-Security` on this hostname, so browsers refuse plain HTTP to it for a year, on every port. Don't also point this hostname at printpaca's LAN address for plain-HTTP access, for example `http://printpaca.example.com:8080`. Reach printpaca on the LAN by IP address or a separate LAN-only name instead.

`printpaca` is the compose service name. Docker Compose resolves it on the internal network the two containers share, so the tunnel reaches the container without using your host's network.

## Running

Set these on the `printpaca` service's `environment:` block in `docker-compose.yml` (uncomment the `environment:` key and the lines beneath it):

```yaml
environment:
  - ALLOWED_HOSTS=printpaca.example.com
  - ADMIN_TRUST_PROXY=1
```

- `ALLOWED_HOSTS` lists every hostname that reaches `PORT`. The Host allowlist covers the front door and the shop too, so a hostname missing from the list gets `403` on every page. A request through Cloudflare Access with a valid token skips the allowlist, but customers don't carry a token.
- `ADMIN_TRUST_PROXY=1` tells printpaca that the tunnel is a single reverse proxy in front of `PORT`. It covers the admin login limits and the customer pages. Without it, the per-IP limits (5 submissions per hour and 60 previews per minute) read `cloudflared`'s container address, so every visitor shares one instance of each budget. Previews run out first: one fires on every debounced keystroke while a visitor fills in an estimate, so once all your visitors together pass 60 previews in a minute, the rest are rejected. Setting it also gives the session cookie the `Secure` flag.

Cloudflare appends the client IP to the end of `X-Forwarded-For`, and printpaca's rate limiter reads the last address, the one Cloudflare appended. A visitor's own `X-Forwarded-For` header only adds earlier entries, which the limiter ignores. This holds only when the tunnel is the single hop between the visitor and printpaca. A second proxy or tunnel that also appends collapses every visitor's last element to that nearer hop's address, and everyone shares one bucket again. Set `ADMIN_TRUST_PROXY=1` only when exactly one such proxy sits in front of printpaca.

Set `CF_TUNNEL_TOKEN` to the token you copied, then start the `tunnel` profile alongside printpaca:

```bash
CF_TUNNEL_TOKEN=your-tunnel-token-here docker compose --profile tunnel up -d
```

To avoid typing the token each time, put it in a `.env` file next to `docker-compose.yml`:

```
CF_TUNNEL_TOKEN=your-tunnel-token-here
```

**Keep `.env` out of version control.** It holds a live tunnel token. This repository's `.gitignore` already excludes it, but check `git status` before committing if you've ever forced it in. Then start the profile without repeating the token:

```bash
docker compose --profile tunnel up -d
```

`docker compose up -d` without `--profile tunnel` never starts `cloudflared`, whether or not `CF_TUNNEL_TOKEN` is set. If you start the profile without a token, `cloudflared` exits with an error naming the missing tunnel and, under `restart: unless-stopped`, keeps restarting until you set the token and recreate it. Check `docker compose logs cloudflared` if the tunnel doesn't come up.

## Verifying

- Open your hostname (for example `https://printpaca.example.com`) in a browser. You should see the front door, served over HTTPS, with an **Order** button and a **Staff sign-in** button. **Staff sign-in** opens `/admin/`.
- Check the connector's own logs for connection status:

  ```bash
  docker compose logs cloudflared
  ```

  A healthy tunnel logs one or more `Registered tunnel connection` lines and no repeated connection errors.

## Protecting the admin app

Choose one of two ways to lock `/admin/` when it's reachable through the tunnel.

- **Cloudflare Access on the path `/admin` (recommended).** Cloudflare asks people to sign in before any request reaches printpaca. Follow [Remote admin access with Cloudflare Access](#remote-admin-access-with-cloudflare-access).
- **The printpaca password alone (supported, not recommended).** With `ADMIN_PASSWORD` set (or generated) and Access not configured, printpaca lets a tunneled request reach `/admin/` and relies on its own login. The printpaca password and its login rate limits are then the only lock between the internet and every client record, invoice, and payment. A weak password, or a flood of guesses spread across many addresses, is a direct path to that data. If you choose this option, set a long `ADMIN_PASSWORD`, set `ADMIN_TRUST_PROXY=1` so the limits see real client addresses, and expect the log line `admin reached through Cloudflare without Cloudflare Access; the printpaca password is the only lock`, written at most once a minute.

If authentication is disabled (`ADMIN_PASSWORD=`) and Access isn't configured, printpaca refuses tunneled `/admin/` requests with `403`. Set a password or configure Access.

The front door, the shop, and `/api/public/*` are anonymous by design and are never subject to this guard.

The one exception to the guard is `DEMO_MODE=1`. A demo instance is read-only, seeded with fake data, and has a published password, so it skips the guard and is intentionally served through the tunnel (see [Run the demo yourself](demo.md)). You can't combine `DEMO_MODE=1` with the Access variables; printpaca refuses to start.

## Isolating the customer pages with PUBLIC_PORT

If you'd rather not run the admin app on the same port as the customer pages, set `PUBLIC_PORT` (for example `8081`) and point the tunnel's hostname at `printpaca:8081` instead. That listener serves the front door, the shop, and the public API, and never serves `/admin/`. Set `PUBLIC_TRUST_PROXY=1` for it, and set `site.adminUrl` (Settings, **Front door and shop**) if the front door should link to an admin hostname you run separately. The two listeners share one set of per-IP limits. See [Public quote page](configuration.md#public-quote-page).

## Remote admin access with Cloudflare Access

Cloudflare Access puts a login in front of a path before any request reaches printpaca, so you can use the admin app from any browser with no VPN app installed. The setup reuses the hostname and `cloudflared` connector you already run for the customer pages, and scopes the Access application to the path `/admin`.

Access is the login. When someone passes it, printpaca accepts the Access token as proof of identity and doesn't ask for a printpaca password. Your printpaca password still works over your LAN or a VPN.

> **Warning:** the Access policy is now the only lock for remote access. A
> policy that admits too many people admits them to the admin app, which
> holds every client record, invoice, and payment. List each person by
> email. Never use an **Everyone** rule or a **Bypass** action.

printpaca verifies the token on every request, so a request that bypasses Access, for example through a misconfigured hostname, gets `403`.

- **Single-user mode** (the default): everyone the policy admits is the admin. A token with no email claim, such as a service token, is answered with `401`, because it doesn't name a person. The web app then shows the password sign-in, and the server log notes the rejected token.
- **Multi-user mode**: printpaca reads the email in the token; see [Multi-user mode](#multi-user-mode).

printpaca resolves identity on every request and never stores it as a session. A person you remove from the policy loses access when their Access session ends. To keep two locks, set `CF_ACCESS_REQUIRE_PASSWORD=1`; see [Keep the printpaca password as a second lock](#keep-the-printpaca-password-as-a-second-lock).

### Set it up

If you haven't added the tunnel hostname yet, create the Access application first. If you already have, follow these steps now; until they're done, the printpaca password is the only lock on `/admin/`.

1. In the Cloudflare dashboard, go to **Zero Trust > Access > Applications**, then select **Add an application > Self-hosted**.
2. Add a public hostname to the application with the hostname you use for printpaca (for example `printpaca.example.com`) and the **path** `/admin`. Scoping the application to `/admin` protects the admin app and its API (`/admin/api/...`) and leaves `/`, `/order`, and `/api/public/*` open to customers.
3. Add a policy with the action **Allow** and the rule **Emails**, and list each person who may reach the admin app. Don't use an **Everyone** rule or a **Bypass** action: either one lets anyone through the outer lock.
4. Set the session duration to 1 week or longer. When an Access session expires, Cloudflare interrupts the installed app with its login page until the person signs in again. A shorter duration limits how long a captured token stays usable; see [Token lifetime](#token-lifetime). In the application's cookie settings, set **Same Site Attribute** to **Lax** (or **Strict**) and turn on **HTTP Only Attribute**.
5. Save the application, then copy its **Application Audience (AUD) Tag** from the application's overview. It's 64 hexadecimal characters.
6. In your tunnel's settings, make sure the **Public hostname** for the same name (`printpaca.example.com`) has service type `HTTP` and URL `printpaca:8080`. Use your `PORT` value if you changed it from `8080`. You don't need a second hostname.
7. In `docker-compose.yml`, uncomment `environment:` if needed and set both variables on the `printpaca` service, then restart it:

   ```yaml
   environment:
     - CF_ACCESS_TEAM=myteam
     - CF_ACCESS_AUD=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
   ```

   `CF_ACCESS_TEAM` is your Zero Trust team name, the part before `.cloudflareaccess.com`. printpaca also accepts `myteam.cloudflareaccess.com` and `https://myteam.cloudflareaccess.com`.

   ```bash
   docker compose up -d
   ```

8. Check the log for `Cloudflare Access: loaded 2 keys for team myteam` (the count can differ), then open `https://printpaca.example.com/admin/`. Pass the Access login and you're in: printpaca doesn't ask for its own password.

You don't need `ALLOWED_HOSTS` for Access traffic to `/admin/`, but customers still do (see [Running](#running)). A request with a valid token skips the Host allowlist, counts as HTTPS for the session cookie, and uses `CF-Connecting-IP` as the client address for the login limits and access log. Requests without a Cloudflare header, such as visits over your LAN or a VPN, behave as before. The token proves Access authorized the caller, not that the request came through Cloudflare, so an authorized person who reaches the admin port directly can send a forged `CF-Connecting-IP` and dodge the per-IP login limit. The global login limit and the printpaca password still apply.

If you add the tunnel hostname before you create the Access application and have set `CF_ACCESS_TEAM` and `CF_ACCESS_AUD`, nothing leaks: requests to `/admin/` arrive without a token and get `403` with the reason `missing`. Without those variables, the printpaca password is the only lock until you configure Access.

printpaca loads Cloudflare's signing keys in the background at startup, so an unreachable Cloudflare never blocks startup or LAN access. Until the keys load, requests through Cloudflare get `403` with the reason `keys_unavailable`. printpaca refreshes the keys hourly and when a token names an unknown key.

### Multi-user mode

In multi-user mode (see [Users](configuration.md#users)), printpaca maps the email in the Access token to a printpaca identity:

- If the email matches the **Your Cloudflare Access email** field in Settings > Users (`users.accessAdminEmail` in `config.yaml`), the person is the admin.
- Otherwise, if an enabled employee has that email in their **Cloudflare Access email** field, the person is that employee, with that employee's permissions.
- Otherwise the person sees the printpaca sign-in screen and can sign in with a username and password. A disabled employee's email counts as no match.

Email comparison ignores case, and each employee email must be unique. Use the address the person signs in to Cloudflare Access with, and add it to your Access policy. A password sign-in in the same browser wins over the token.

Turning multi-user mode off makes everyone the policy admits the admin again, with one exception: a token whose email matches any employee row, enabled or disabled, gets no identity and sees the sign-in screen, so a current or former employee never becomes the admin by that switch. If the lookup fails, printpaca refuses the token too. To admit such a person as the admin, clear or change their email in Settings > Users first.

printpaca refuses to save an employee email equal to **Your Cloudflare Access email**, and the reverse. Emails must be plain ASCII with no spaces, and printpaca lowercases only A to Z when comparing, so look-alike characters never match.

The mapping trusts the email claim from your identity provider. Use providers that verify email addresses (Cloudflare's one-time PIN does) and explicit **Emails** policies, not broad rules such as a whole email domain.

### Token lifetime

A captured Access token is a full credential until it expires. Revoking the person's session in Cloudflare doesn't invalidate a token already issued, and anyone holding one can replay it at the admin port on your LAN. A shorter session duration shrinks that window; a longer one is more convenient for the installed app.

printpaca also refuses an unsafe request (`POST`, `PUT`, `PATCH`, `DELETE`) authenticated only by the token unless it carries an `Origin` header for the same host or `Sec-Fetch-Site: same-origin`. Browsers send one on their own requests.

### Sign out

When you're signed in through Access, the sidebar shows **Sign out of Cloudflare Access**, which ends your Access session. The Account card shows your email and hides the change-password form. To change the admin password, set `ADMIN_PASSWORD` and restart. An employee can change their password after signing in over the LAN, or an admin can reset it from Settings > Users.

### Keep the printpaca password as a second lock

Set `CF_ACCESS_REQUIRE_PASSWORD=1` on the `printpaca` service to require both the Access token and a printpaca session. A verified request without a session gets `401` and the sign-in screen. Accepted values are unset, `0`, and `1`; anything else stops startup. It can't be combined with `ADMIN_PASSWORD=` (empty), because no session could exist; printpaca refuses to start.

### Troubleshooting

A rejected request gets a `403` with a one-line plain-text body, and the server log records the reason (at most one line a minute for each reason).

| Reason | Cause | Fix |
|---|---|---|
| `missing` | The request has no Access token. No Access application protects this path. | Create the Access application for the hostname and the path `/admin`, matching the tunnel's public hostname. |
| `malformed` | The token isn't a well-formed JWT. | Sign in again through the Access login page. If it persists, check that no other proxy rewrites headers. |
| `algorithm` | The token uses a signing algorithm other than RS256. | Cloudflare Access signs with RS256. Something else is sending the header. |
| `unknown_key` | The token is signed with a key that isn't in your team's key set. | Check `CF_ACCESS_TEAM`. |
| `signature` | The token signature is invalid. | Check `CF_ACCESS_TEAM`. |
| `issuer` | The token was issued by a different team. | Check `CF_ACCESS_TEAM`. |
| `audience` | `CF_ACCESS_AUD` doesn't match the Access application on this path. | Copy the AUD tag from that application, and make sure the hostname and path belong to the application you copied it from. |
| `expired` | The token has expired. | Reload to sign in again. If it persists, check the server clock. |
| `not_yet_valid` | The token's start time is in the future. | Check the server clock. |
| `keys_unavailable` | printpaca hasn't loaded your team's signing keys. | Check that the container can reach `https://<team>.cloudflareaccess.com`, and check the startup log for the failure. |

If printpaca refuses to start with a message naming `CF_ACCESS_TEAM`, `CF_ACCESS_AUD`, or `CF_ACCESS_REQUIRE_PASSWORD`, fix the named variable. Setting only one of the first two, or an invalid value in any, stops startup on purpose.

## Other ways to reach the admin app remotely

Cloudflare Access is the lowest-friction option. Two alternatives need an app on each device but don't depend on Cloudflare: a VPN (WireGuard or Tailscale) into your home network, or an SSH tunnel to `PORT`.

You can also put a non-Cloudflare, TLS-terminating reverse proxy (nginx, Caddy) on your LAN in front of `PORT`. Follow [Running behind a reverse proxy](configuration.md#running-behind-a-reverse-proxy-https) in full: it covers `ADMIN_TRUST_PROXY=1` (so the session cookie gets `Secure`) and `ALLOWED_HOSTS` (so the listener accepts the proxy's hostname). Skipping either doesn't fail obviously: the cookie loses `Secure`, or every request returns `403`.

## See also

- [Configuration](configuration.md) for the routing table, `site.*`, `PUBLIC_PORT`, the item gallery, and the reverse-proxy guide referenced above.
- [Getting started](getting-started.md) for installing Docker and running printpaca for the first time.
