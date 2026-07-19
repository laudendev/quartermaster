# API Reference

Four HTTP endpoints across two listeners. See `docs/architecture.md`
for which listener is public and which is WireGuard-only.

## Public listener (behind Caddy, `quartermaster.<domain>`)

### `POST /stripe/webhook`

Receives Stripe's `checkout.session.completed` event. Not intended
to be called by anything other than Stripe.

**Headers required:**
- `Stripe-Signature` — Stripe's HMAC signature header. Requests
  without a valid signature are rejected before the body is parsed.

**Behavior:**
- Verifies `Stripe-Signature` over the raw request body. Signature
  must be within a 5-minute window of the current time (replay
  defense).
- Only `checkout.session.completed` events are acted on. Other event
  types are acknowledged (`200`) and ignored.
- Billing country must be `US` (case-insensitive). Non-US checkouts
  are acknowledged (`200`) but never enqueued.
- `metadata.seats` is parsed as an integer, floored to `1` if zero,
  negative, or unparseable, and rejected outright if greater than
  `24` (acknowledged with `200`, never enqueued, logged as a likely
  metadata-integrity problem).
- On success, enqueues a sign request keyed by the Stripe session ID
  (idempotent — a retried webhook with the same session ID is
  absorbed, not duplicated).

**Responses:**
| Status | Meaning |
|---|---|
| `200` | Acknowledged. May or may not have enqueued — see body-independent conditions above. Always `200` for anything Stripe should not retry. |
| `400` | Missing/invalid `Stripe-Signature`, or malformed JSON body. |
| `500` | Internal error (enqueue failure). Stripe will retry. |

No response body is meaningful in any case — Stripe does not read it.

---

### `POST /license/activate`

Called by the customer's application on first run.

**Request body (JSON):**
```json
{
  "license_key": "string, base32 license key as issued",
  "fingerprint": "string, see docs/license-scheme.md for derivation"
}
```

**Behavior:**
1. Both fields required and non-empty, or `400`.
2. License signature verified against the embedded public key. Any
   verification failure (malformed, unrecognized version, bad
   signature) returns the same generic `401` — deliberately not
   distinguished, so a failing request can't be used to probe which
   check failed.
3. Revoked licenses are refused with `403`, regardless of seat
   availability.
4. If this exact `(license_id, fingerprint)` pair is already active,
   the call succeeds as a no-op (idempotent — this is the normal
   path on every app launch after the first).
5. Otherwise, if the license's seat count is already reached,
   refused with `409`.
6. Otherwise, the activation is recorded and the call succeeds.

**Responses:**
| Status | Meaning |
|---|---|
| `200` | Activated (or already active on this fingerprint — same result either way). |
| `400` | Missing `license_key` or `fingerprint`. |
| `401` | License key failed verification. |
| `403` | License is revoked. |
| `409` | Seats exhausted; this fingerprint is not among the already-activated ones. |
| `500` | Internal error (store read/write failure). |

---

### `POST /license/deactivate`

Called by the customer's application when the user explicitly
deactivates.

**Request body (JSON):** identical shape to `/license/activate`.

**Behavior:**
1. Both fields required and non-empty, or `400`.
2. License signature verified — same generic `401` on any failure.
3. The `(license_id, fingerprint)` activation row is deleted if
   present. Deleting a row that doesn't exist is not an error —
   this call is safe to retry or call speculatively.
4. No revocation check — a revoked license can still be deactivated;
   there's nothing meaningful to protect by refusing this.

**Responses:**
| Status | Meaning |
|---|---|
| `200` | Deactivated (or was already inactive — same result either way). |
| `400` | Missing `license_key` or `fingerprint`. |
| `401` | License key failed verification. |
| `500` | Internal error (store write failure). |

## WireGuard-only listener (`10.46.0.1:9090`, unreachable from the public internet)

Used exclusively by the signer. Never exposed to Caddy or the public
interface.

### `GET /queue/wait`

Long-polls for the next pending sign request. Blocks up to 55
seconds server-side. Safe to call repeatedly in a loop — this is
exactly what the signer does.

**Responses:**
| Status | Meaning |
|---|---|
| `200` | A pending request was found. Body is a JSON `SignRequest` (`id`, `product`, `email`, `seats`). |
| `204` | No pending work within the poll window. Caller should immediately call again. |
| `500` | Internal error. |

### `POST /queue/complete`

Reports the result of signing (or refusing to sign) a request.

**Request body (JSON), success case:**
```json
{ "id": "string, the SignRequest.id from /queue/wait", "license_key": "string, the issued license" }
```

**Request body (JSON), rejection case:**
```json
{ "id": "string, the SignRequest.id from /queue/wait", "reject_note": "string, human-readable reason" }
```

Exactly one of `license_key` or `reject_note` should be present.
`reject_note` takes precedence if both are somehow set.

**Behavior:**
- On `reject_note`: marks the row rejected, stores the note. No
  email is sent.
- On `license_key`: marks the row signed, stores the key, and — if
  the row transitioned (i.e., wasn't already handled) — sends the
  license by email. A repeated call with the same `id` after it's
  already signed is a no-op that does not resend the email.
- Email delivery failure is logged but does not fail the request —
  the row is still marked signed either way, and the license key is
  safely stored in the `sign_requests` row regardless of whether the
  email arrived. There is no automatic retry or resend on failure;
  a failed send (bad address, mail provider issue) requires manually
  looking up the stored key and re-sending or relaying it to the
  customer. See `docs/operations.md`.

**Responses:**
| Status | Meaning |
|---|---|
| `200` | Accepted — reject or complete both return `200` on success, including no-op repeats. |
| `400` | Missing `id`, or neither `license_key` nor `reject_note` present. |
| `500` | Internal error (store write failure). |
