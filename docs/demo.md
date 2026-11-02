# Run the demo yourself

printpaca ships a read-only demo mode, seeded with fake data, so you can look around without entering your own clients or prices. `DEMO_MODE=1` is in the normal image behind a flag that is off by default, and [`docker-compose.demo.yml`](../docker-compose.demo.yml) turns it on.

Don't use demo mode for your own deployment. It waives the admin password length floor and disarms the Cloudflare tunnel guard.

## What the demo does

- Read-only admin: every change is blocked with a friendly message. Sign in with the password `demo` (shown on the sign-in screen).
- Seeded history: on first start, about six months of fake clients, quotes, invoices, payments, and requests, plus three gallery items with generated images and one item order.
- Customer pages (front door at `/`, shop at `/order`): the real customer flow, but nothing is stored.
- Spoolman and Moonraker pages render fake sample data.
- Multi-user mode with two employees, `sam` (quotes, invoices, clients, dashboard) and `riley` (items, requests, filament), both with the password `demo`.
- The demo banner offers per-visitor display preferences (theme, currency, single-user or multi-user view). They stay in the visitor's browser.

## Run it

```bash
mkdir -p ./demo-config && sudo chown 65532:65532 ./demo-config
docker compose -f docker-compose.demo.yml up -d
```

Then open:

- Admin app: `http://127.0.0.1:8080/admin/`
- Front door: `http://127.0.0.1:8080/`
- Customer-only listener: `http://127.0.0.1:8081/` (front door) and `http://127.0.0.1:8081/order` (shop)

To reseed, stop the container, delete `./demo-config`, and start it again. `DEMO_MODE=1` starts only against an empty data directory or one the demo seeded itself. Any other existing database makes the container crash-loop by design.

## Serve it through a Cloudflare Tunnel

The demo has its own tunnel service, separate from any production tunnel, so a demo outage or ingress mistake never touches it.

1. In Zero Trust, create a tunnel (for example `printpaca-demo`) and copy its token.
1. Add a public hostname for the demo with the service `http://printpaca:8080`. The admin app is at `/admin/` on that hostname. The server permits it through the tunnel without Cloudflare Access only because `DEMO_MODE=1` disarms the tunnel guard.
1. Optionally add a second hostname for the customer-only listener pointed at `http://printpaca:8081`. The `PUBLIC_PORT` listener has no `Host` allowlist, so that hostname needs no other change.
1. Put the values in a `.env` file next to the compose file, and keep it out of version control:

   ```bash
   CF_TUNNEL_TOKEN=<token>
   DEMO_HOSTNAME=<the hostname from step 2>
   DEMO_PUBLIC_URL=<optional https URL of the customer-only hostname>
   ```

1. Start the tunnel profile:

   ```bash
   docker compose -f docker-compose.demo.yml --profile tunnel up -d
   ```

The compose file already sets both trust-proxy flags. See [Cloudflare Tunnel](cloudflare-tunnel.md) for how the tunnel guard works.
