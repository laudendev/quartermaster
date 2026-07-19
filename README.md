# Quartermaster

I want to sell software. I don't want to give away a slice of every
sale to a licensing vendor, forever, for the privilege. I looked at
what's out there and rejected all of it — every option gets *more*
expensive as you succeed, which is exactly backwards. Fixed
infrastructure costs should stay fixed. If a licensing platform's
bill grows because I sold more copies, that platform is charging me
for succeeding, not for the service it provides.

None of them are built for one person, either. They assume a team, a
support org, a compliance department — overhead a solo developer
doesn't have and doesn't need. So instead of settling, I built the
thing I actually wanted: Stripe handles the money, and everything
after that — issuing the license, verifying it's genuine, enforcing
how many machines can run it — is mine, self-hosted, sized for
exactly one person to run, and costs the same flat amount whether I
sell ten licenses or ten thousand.

The whole thing is built around one hard security guarantee: the
machine exposed to the internet can never forge a license, and the
machine that can forge a license is never reachable from the
internet.

This isn't a toy or a tutorial project. It's built and tested with
the rigor of real production software — real trust boundaries, real
test coverage of every one of them, a real deployed Stripe
integration verified end to end against a live checkout — ready to
issue real licenses to real customers.

## What it does

Reacts to confirmed Stripe payments, issues Ed25519-signed licenses,
enforces per-seat activation, and delivers the result by email — no
per-transaction fee beyond Stripe's own, no per-user cap, no
dependency on any third-party licensing vendor's uptime.

## Why the architecture looks like this

The private signing key — the one thing that can mint a valid
license — lives on a machine that only ever *initiates* outbound
connections and never accepts inbound ones. If the public-facing
server is ever fully compromised, an attacker gets a logged,
rate-limited queue to submit *requests* to — never the ability to
sign anything. This tradeoff (two machines and a tunnel, instead of
one simpler server) is deliberate: it makes the worst-case failure
mode structurally impossible rather than merely unlikely.

The license format itself is a fixed-width, versioned binary payload
designed as a permanent ABI — a license issued today is meant to
still verify correctly, offline, against a public key embedded in an
app, years from now, with no server and no network call required.
Verification is multi-key by design from the start, so the signing
key can be rotated in the future without ever invalidating a license
already sold.

See [docs/architecture.md](docs/architecture.md) for the full design.

## How licensing actually treats the customer

Most licensing platforms treat every activation as suspicious by
default — lock a key to one machine forever, require a support
ticket to reinstall, make resale effectively impossible. I built the
opposite of that, on purpose.

A license here behaves like a physical good. Deactivate it, and the
seat is free — hand it to someone else, they activate fresh, no
different from reselling a used copy of anything else. The platform
never tries to track *who* owns a license after the first sale, only
*how many machines* are using it at once. A refund or a chargeback
doesn't retroactively break a machine that's already running
legitimately, either — that's treated as an accepted, bounded cost,
not a bug to engineer away.

What it does stop, hard: one key posted publicly and claimed by an
unlimited number of strangers at once. The moment more distinct,
never-before-seen machines try to activate than the license has
seats for, the request is refused outright — the seat count is the
actual enforcement, not machine-tracking, not phone-home, not
periodic re-validation.

And once a machine is activated, that's it — verification is fully
offline, forever, for every subsequent launch. No check-in, no
expiry, no dependency on my server staying up. The one and only
network call in the entire lifecycle is the first activation itself.

Both halves rest on the same idea: the server only ever needs to
know *how many* machines are using a license, never *which* person
owns it. That's what makes it possible to be generous about resale
and reinstalling while still being strict about the one thing that
actually costs money — mass key sharing. The full reasoning, and
what it deliberately does and doesn't prevent, is in
[docs/license-scheme.md](docs/license-scheme.md).

## If you're building something like this

This wasn't built as a teaching example, but it holds up as one,
because every piece of it is real: real money moving through a real
Stripe integration, a real cryptographic trust boundary enforced by
actual machine separation rather than a config flag, real seat
enforcement against a real database, tests that exercise the actual
failure modes rather than just the happy path.

If you're trying to figure out how to sell software as one person —
how to structure the trust boundary between a public server and a
signing key, how to design a license format that has to keep working
for years with no server behind it, how to handle Stripe webhooks
correctly (signature verification, replay defense, idempotency) —
`docs/architecture.md` and `docs/license-scheme.md` walk through the
actual reasoning, not just the code. The reasoning is the part
worth taking.

## Documentation

- **[docs/architecture.md](docs/architecture.md)** — the two-program
  split, the security property the whole design rests on, and how a
  purchase flows end to end.
- **[docs/license-scheme.md](docs/license-scheme.md)** — the
  normative spec: payload byte layout, seat rules, product code
  format, fingerprint derivation, activation state machine, and the
  full threat model.
- **[docs/api.md](docs/api.md)** — every HTTP endpoint, request and
  response shapes, status codes.
- **[docs/operations.md](docs/operations.md)** — deploy, key
  ceremony and rotation, backups, incident response, support
  runbooks.

## Components

| Path | What it is |
|---|---|
| `cmd/quartermaster` | The public-facing service: Stripe webhook, activation API, queue API. Runs on the droplet. |
| `cmd/signer` | Polls the queue, signs licenses with the private key. Runs on trusted hardware only — never the droplet. |
| `cmd/keygen` | One-time Ed25519 keypair generation for `signer`. |
| `license` | The signed license format itself — construction, signing, verification. |
| `queue` | Owns the `sign_requests` table: Stripe → signer handoff. |
| `activations` | Owns the `activations` table: per-seat activation, deactivation, revocation. |

## Development

```bash
go test ./...
```

Every trust boundary has real test coverage — signature verification
(valid, tampered, wrong key, unrecognized version), webhook business
logic, the queue's concurrency behavior, and the activation model's
seat math.

## Deploy

```bash
./deploy.sh
```

Run from the signer machine, not the droplet — see
[docs/operations.md](docs/operations.md) for why, and for the full
deploy, key ceremony, and backup story.

## On AI-assisted development

As of mid-2026, I used AI tools — specifically Claude Sonnet 5 and
Claude Fable 5 — throughout this project's design and implementation,
for code review, documentation drafting, and working through design
tradeoffs. I make no apology for that; refusing a tool that produces
genuinely good results because of how it looks would be foolish, not
principled.

Nothing here runs AI. Every binary in this repository is deterministic
Go, compiled once and executed exactly as written — there is no model
in the loop at runtime, on the droplet, on the signer, or anywhere in
the request path. AI was a drafting tool used during construction,
the same category of tool a compiler is: it helped translate decisions
into working syntax. The decisions — the split-key architecture, the
seat rules, the payload's byte layout and why it's versioned the way
it is — are mine.

And I didn't accept any of it on faith. Every line was verified, not
assumed — including going through the Go standard library's own
base32 implementation byte by byte to understand exactly what it
does and why, rather than trusting a summary of it. When AI-suggested
test code had real bugs, and it did, those were caught by actually
running the tests. When an AI-drafted license file turned out to be
materially wrong, that was caught too, by fetching and diffing
against the actual canonical source before it ever shipped.

This is not vibe-coded. It's AI-assisted and rigorously verified —
and it's the only way I'd use these tools on something customers
pay for.

## License

[PolyForm Noncommercial 1.0.0](LICENSE). Free to read, learn from,
and use for any noncommercial purpose — personal projects, research,
education, nonprofits, government. Commercial use requires a
separate agreement.
