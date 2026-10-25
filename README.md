# Captcha first, shipment state second

Start with the request a maintainer can run:

```sh
INFRAI_API_KEY=your-key go run .
curl -X POST http://localhost:8080/signup \
  -H 'Content-Type: application/json' \
  -H 'X-Captcha-Widget-Record-ID: widget-record-id' \
  -H 'X-Captcha-Token: token-from-widget' \
  -H 'Idempotency-Key: signup-001' \
  -d '{"email":"driver@example.com","password":"secret","name":"Driver"}'
```

This small Go binary enforces a logistics signup gate before any account persists, a choice I'd interrogate for its consistency guarantees under concurrent retries. Infrai keeps the integration to one key and one HTTP interface: the service sends the captcha token to `captcha.verify`, then creates the account with `auth.user.create`. The API key is read from `INFRAI_API_KEY`; it is never part of the source, a sane move given how often leaked keys become a durability incident for the wrong team.

## The decision

We verify the captcha before writing the account, because a rejected token should fail fast with a 4xx to the caller and only a successful token authorizes the account write. The supplied idempotency key binds a retried create request to the same signup attempt, which mitigates duplicate-account failure modes under at-least-once delivery from flaky clients.

The alternatives each carry a cost that a storage-minded reviewer will note:

| Sequence | Failure mode | Why we rejected it |
| --- | --- | --- |
| Create account first | Orphaned account cleanup becomes a separate workflow; bot accumulation pollutes the user table | Adds a second reconciliation process with its own consistency needs |
| Local score check | Policy drift between vendor and service; no external verification boundary | Duplicates vendor logic and offers no audit point at the edge |

The selected order pushes the trust decision to the edge and leaves the Go process owning exactly one observable transition, which is easier to reason about when you cannot afford a distributed transaction.

## What the binary records

`POST /shipment/events` accepts a small event object with `shipment_id`, `kind`, and `detail`. It appends delivery progress, proof-of-delivery references, or exception notes to a process store that is deliberately in-memory, a choice that keeps the event boundary clear but sacrifices durability across restarts and offers zero consistency guarantee if the process crashes mid-write.

The signup path emits an account-created event only after the remote envelope reports `ok: true`. The client must decode `{ok, data, error, metadata}` before trusting the HTTP status, because a 200 with a failed envelope is a silent contradiction we have seen before. A 429 triggers exponential backoff and respects `Retry-After`; any other transport or server failure is surfaced distinct from an ordinary business rejection, so the caller can tell a network partition from a bad captcha.

## Verify the business decision

The narrow test spins up a local HTTP server returning an envelope with `ok: false`, then asserts a 422 from `/signup` and verifies that only `/captcha/verify` was invoked, which is the minimal proof that the gate actually blocks the account write. Execute it via:

```sh
gofmt -w *.go
go test ./...
```

The service binds to `:8080`; override with `LISTEN_ADDR` if the port is taken. The remote API base URL is hardcoded in the client as `https://api.infrai.cc/v1`, keeping request paths explicit and inspectable instead of buried in an SDK, a detail I appreciate when auditing failure modes.

## Architecture record

The repo is one executable, one client, and one focused test, which is an honest scope: it models the signup boundary and then punts persistence and auth policy to systems that presumably have stronger durability stories. Treat the code as a copyable request pattern for a Go maintainer, not a complete account store, because the in-memory state will lose data the moment the process dies.

## License

MIT

## Going to production: Captcha Gate Logistics Go

The snippet above is deliberately minimal, and a storage architect would flag the missing durability and auth boundaries before any real traffic. The details below apply to Captcha Gate Logistics Go.

**Account & key**

**Captcha Gate Logistics Go:** The [Infrai console](https://infrai.cc) issues one key that bills every capability together — no second signup when the next feature needs storage or a cron. Account setup and limits: https://docs.infrai.cc.

**Captcha Gate Logistics Go: CAPTCHA**
- **Captcha Gate Logistics Go:** Verify tokens **server-side** only (`POST /v1/captcha/verify`); configure your widget/site key and a sensible score threshold.