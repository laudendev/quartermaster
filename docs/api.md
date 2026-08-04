
# API Reference

Five HTTP endpoints across three listeners. See `docs/architecture.md`
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
- Fetches the session's finalized line items directly from Stripe
  (`GET /v1/checkout/sessions/{id}/line_items`) rather than trusting
  webhook metadata for purchase contents — the webhook payload itself
  never carries expandable fields, and a cart may contain several
  distinct products in one checkout.
- Sums quantity across every line item. If the total exceeds `24`
  units, the whole session is rejected outright (acknowledged with
  `200`, nothing enqueued, logged as a likely integrity problem) —
  the same ceiling as before, now enforced against the real Stripe
  data instead of a metadata field the caller controlled.
- For each unit of each line item, resolves the Stripe Price ID to a
  product code via softstore's internal API (see "Fetching a product
  code" below), then enqueues one single-seat sign request per unit.
  A cart with two of Product A and one of Product B produces three
  separate sign requests, three separate licenses — not one license
  with a seat count of three. This was a deliberate choice: seats
  represent simultaneous machines for *one* license, not units
  purchased in one order.
- Each unit's sign request is keyed `{stripe_session_id}#{index}`
  (idempotent per unit — a retried webhook reproduces the same keys
  and each is absorbed, not duplicated; different units in the same
  session never collide with each other).
- Once every unit in a session has been signed, the combined receipt
  email is sent exactly once for the whole session — see
  "Combined receipt" below. Cart-based checkouts also carry a
  `metadata.cart_token`; once fulfillment for that session completes,
  the token is used to clear the corresponding cart on softstore via
  its internal API.

**Responses:**
| Status | Meaning |
|---|---|
| `200` | Acknowledged. May or may not have enqueued — see body-independent conditions above. Always `200` for anything Stripe should not retry. |
| `400` | Missing/invalid `Stripe-Signature`, or malformed JSON body. |
| `500` | Internal error — fetching line items from Stripe failed, a product-code lookup against softstore failed, or an enqueue failed. Stripe will retry the whole webhook; already-enqueued units from a partial failure are safely re-absorbed by the idempotent per-unit keys, not duplicated. |

No response body is meaningful in any case — Stripe does not read it.

#### Fetching a product code

Quartermaster has no product catalog of its own — a Stripe Price ID
is meaningful only to softstore, which owns the `product_code ↔
stripe_price_id` mapping. Rather than duplicate that mapping (and
have it drift out of sync every time a product is added), the
webhook calls softstore directly: `GET
/internal/products/by-price/{price_id}` over the WireGuard tunnel,
authenticated with a shared secret (`X-Internal-Secret`). See
`docs/architecture.md` for why this lookup happens over WireGuard
rather than softstore's public hostname.

#### Combined receipt

A cart of several distinct products completes as several sign
requests, signed independently by the signer, often at slightly
different times. Emailing a separate receipt per item would mean a
buyer of three products gets three unrelated-looking emails for one
purchase. Instead, every time a unit finishes signing, quartermaster
checks whether every other unit belonging to the same Stripe session
has also finished; only once the whole session is complete does it
fetch the session's full line-item detail from Stripe (names,
per-item pre-tax price, tax) and send one email listing every
product and its license key together, with a single combined total.
This check-on-every-completion happens both inline (right after a
unit is signed) and on a periodic retry sweep, so a session that was
signed while quartermaster was briefly down still gets its email once
it comes back.

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


## WireGuard-only listener (`10.20.0.2:6774`, unreachable from the public internet)

A second, distinct WireGuard tunnel from the one used by the signer
(`10.46.0.0/24`) — this one connects quartermaster-prod directly to
softstore-prod, droplet to droplet, and carries only this one
endpoint. See `docs/architecture.md` for why this is a separate
tunnel rather than reusing the signer's.

### `GET /internal/sessions/{session_id}/status`

Called by softstore's thank-you page, polled every few seconds while
a customer waits after checkout, so the receipt and license keys can
appear on the page itself rather than requiring the customer to wait
for email delivery.

**Headers required:**
- `X-Internal-Secret` — shared secret, compared in constant time.
  Requests without the correct value are rejected with `401` before
  any lookup happens.

**Behavior:**
- Looks up every `sign_requests` row for the given session ID.
- If no rows exist for that session at all, `found` is `false`.
- If any row is still `pending`, `ready` is `false` and no item
  detail is returned — the poller is expected to call again shortly.
- Once every row for the session is `signed`, `ready` is `true`, and
  the response includes the same combined-receipt detail as the
  email (product name, pre-tax price per item, tax, total) — built
  by the same shared code path as the email, so the two can't drift
  out of sync with each other.

**Response body (JSON):**
```json
{
  "found": true,
  "ready": true,
  "items": [
    { "product_name": "string", "amount_line": "string, formatted with currency symbol", "license_key": "string" }
  ],
  "tax_line": "string, formatted with currency symbol, omitted if zero",
  "total_line": "string, formatted with currency symbol"
}
```

`items`, `tax_line`, and `total_line` are omitted entirely while
`ready` is `false`.

**Responses:**
| Status | Meaning |
|---|---|
| `200` | Always, whether or not the session is ready — see body for actual status. |
| `400` | Missing session ID in the path. |
| `401` | Missing or incorrect `X-Internal-Secret`. |
| `500` | Internal error (store read failure). |
