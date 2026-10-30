# Operations

Deploy steps, key ceremony, backups, secret rotation, and incident
response. This is the doc for "something is on fire, what do I do."

## Deploy

`./deploy.sh`, run from the repo root on jellyjamz
(`~/lauden.dev/quartermaster`). jellyjamz holds no signing key; it only
builds and ships binaries.

1. Runs the full test suite (`go test ./...`). Aborts on any
   failure — nothing broken ships.
2. Cross-compiles `quartermaster` for qmaster
   (`CGO_ENABLED=0 GOOS=linux GOARCH=amd64`), `scp`s it to a staging
   directory, and an `inotifywait`-based watcher on qmaster detects
   the new binary and redeploys automatically. Verifies the service
   is active afterward.
3. Cross-compiles `signer` as a static linux/amd64 binary
   (`-trimpath -ldflags="-s -w"`), records its sha256, `scp`s it to
   `qm-signer:/tmp/qm-signer.new` over the WireGuard admin tunnel,
   verifies the checksum on the droplet, installs it to
   `/usr/local/bin/qm-signer` (root-owned, mode 755), restarts
   `qm-signer.service`, and verifies it is active. It prompts for
   the droplet sudo password.

Both binaries ship from one script, one run. The qmaster restart and
the signer restart each interrupt service for a second or two; open
checkout pages keep polling and recover.

**`deploy.sh` does not copy public keys to qmaster.** If a key is
added or rotated, copy the new public half to
`/opt/quartermaster/` and restart `quartermaster` separately — see
"Rotating the key" below.

## Signer droplet (`qm-signer`)

The signing key lives only on the DigitalOcean droplet `qm-signer`.
jellyjamz no longer signs, so it can sleep without stopping fulfillment.

- **Access:** `ssh qm-signer` (10.30.0.1) over the WireGuard admin
  tunnel `qm-signer` (10.30.0.0/24, UDP 51830). nftables is
  default-drop and accepts SSH only on that interface; the DO Cloud
  Firewall exposes only UDP 51830. If the tunnel is lost, recover from
  the DO web console.
- **Queue path:** the droplet reaches qmaster over `wg-qmaster`
  (10.46.0.3 to 10.46.0.1), long-polls `/queue/wait`, signs, and POSTs
  `/queue/complete`. Complete is first-writer-wins and returns 409 if
  the request is no longer pending.
- **Keys:** `/etc/qm-signer/active.key` (loaded by the unit through
  systemd `LoadCredential`) and `standby.key`, with matching `.pub`
  files. Qmaster and app 0.1.19 and later trust both public keys.
  Private halves are backed up only in KeePassXC. Never enable DO
  Backups or take snapshots of this droplet: they would copy the key
  off the box.
- **Unit:** `qm-signer.service` runs `/usr/local/bin/qm-signer` as a
  DynamicUser with the empty capability set, `ProtectSystem=strict`,
  and an `IPAddressAllow` list limited to qmaster.
- **Monitoring:** qmaster emails an alert if the signer has not polled
  the queue for 10 minutes, and a recovery notice when it resumes.
  The stale-request alert still fires for pending requests that sit
  unsigned.
- **Not yet done:** a full reboot test of the droplet (default route,
  both tunnels, nftables, `qm-signer`).

## Key ceremony

The signing keypair is generated on the signer droplet, never on
jellyjamz. The private key is written to `/etc/qm-signer/` (directory
mode `0700`, root-owned) and backed up in KeePassXC; the public half
is copied to qmaster.

**The private signing key never leaves the droplet except into the
KeePassXC backup, under any circumstance.** It is not committed to git
(`.gitignore` enforces this), not covered by DO Backups or snapshots,
not emailed, not pasted into a chat tool. If a private key is ever
exposed, treat it as a full compromise — see "Incident: signing
key exposure" below.

**Historical failure mode (the signer used to run on jellyjamz).**
The key was gitignored by design, so a fresh clone, a history scrub,
or a directory reorganization never carried it along, and the signer
failed to start (`loading signing key: open signing.key: no such file
or directory`). This exact failure occurred on 2026-07-19 during a git
history rewrite that swapped the working directory — see "Observed
in practice" below.

`signing.pub` is not sensitive. It needs to exist in three places:
- On the droplet, at `/opt/quartermaster/signing.pub`, loaded by
  `quartermaster` at startup (`activation.go`'s
  `activationAPI.pubs`) to verify licenses during activation.
- Embedded in every customer-facing application, to verify licenses
  fully offline.
- Recorded below, so the current key is always known from this
  document alone.

### Current signing key

| Public key (hex) | Live since |
|---|---|
| `eba29494abda910c3670ab0aab126cbca5062130f54c3ad0bcbc9d5aa8d6b9ca` | 2026-07-19 |

No real license has been issued under any prior key, so there is no
rotation history to preserve yet — this is simply the current key,
recorded here for reference. Once a real rotation happens after real
licenses exist, this section becomes a full rotation history table
(see "Rotation model" below for what that will need to track: every
key that was ever live, its date range, and why it was rotated).

### Rotation model

The signing key can be rotated without invalidating previously-issued
licenses — they keep verifying forever, per `docs/license-scheme.md`.

This works because verification is multi-key by design, on both
sides:

- **Droplet**: `activationAPI.pubs` is a `[]ed25519.PublicKey`, and
  `activation.go` calls `license.VerifyAny(a.pubs, key)`, which
  tries each embedded key in order and succeeds if any one of them
  matches. A license signed under any key that was ever added to
  this list still activates correctly.
- **Client apps**: must embed the same list and use the same
  try-each-key verification (`license.VerifyAny`, or an equivalent
  implementation of the same logic if the client isn't written in
  Go). This is a hard requirement for any client shipped from this
  point forward — an app that only checks a single hardcoded public
  key cannot be safely fixed after the fact without breaking every
  license issued before the fix ships.

`VerifyAny` short-circuits on `ErrMalformed` or `ErrVersion` (these
don't depend on which key is used, so trying more keys can't fix
them) and only continues trying additional keys on `ErrSignature`.
See `license/license_test.go` for the behavior this rotation model
depends on.

### Rotating the key

1. Generate a new keypair on the signer droplet (never on jellyjamz) and back up the private key in KeePassXC before using it.
2. If real licenses exist under the current key, add the new key to
   a rotation history table (promote the single-row table above into
   a full history) and append it to `activationAPI.pubs` in
   `cmd/quartermaster/main.go` — keep the old key too, never remove
   a historical key once real licenses depend on it. If no real
   licenses exist yet, a clean single-key replacement is fine, as
   was done on 2026-07-19.
3. Copy the new public key to qmaster as
   `/opt/quartermaster/signing2.pub` (mode 0644; `deploy.sh` does not
   do this), then restart the server:
````bash
   ssh qmaster "sudo systemctl restart quartermaster"
```
4. Ship a client app update that adds the new public key to its own
   embedded list, alongside every previous key (only relevant once
   real clients exist and real licenses have been issued under a
   prior key).
5. Make it the signer's `active.key` on the droplet (the unit loads that file) and restart `qm-signer`; new licenses are then signed with it.

## Backups

`activations`, `sign_requests`, and `session_emails` are the
business — losing this database means losing the record of who has
activated what, any license keys not yet delivered, and which
sessions have already had their combined receipt email sent (a
restore from an older backup can under-count `session_emails`,
meaning a customer whose email already went out gets a duplicate on
the next retry pass — annoying, not dangerous, since the email itself
is idempotent-safe to receive twice, but worth knowing about rather
than being surprised by after a restore).

### Why a raw file copy is not safe

`quartermaster.db` runs under `PRAGMA journal_mode=WAL`. Recent
writes may exist only in a companion `-wal` file until SQLite
checkpoints them back into the main file. A plain `cp
quartermaster.db backup/` taken while the service is running risks
capturing the main file without its matching WAL contents — a
backup that looks complete but is silently missing recent
transactions.

### Droplet-side snapshot (systemd timer)

`/etc/systemd/system/quartermaster-backup.service`:
```ini
[Unit]
Description=Quartermaster database backup (WAL-safe snapshot)

[Service]
Type=oneshot
User=quartermaster
ExecStart=/bin/sh -c '/usr/bin/sqlite3 /opt/quartermaster/quartermaster.db ".backup /opt/quartermaster/backups/quartermaster-$(date +%%F).db" && /usr/bin/find /opt/quartermaster/backups/ -name "*.db" -mtime +90 -delete'
```

`/etc/systemd/system/quartermaster-backup.timer`:
```ini
[Unit]
Description=Run quartermaster-backup daily

[Timer]
OnCalendar=*-*-* 02:00:00
Persistent=true

[Install]
WantedBy=timers.target
```

Enable with `sudo systemctl enable --now quartermaster-backup.timer`.

Backups older than 90 days are pruned automatically, chained with
`&&` so a failed backup never triggers cleanup of existing snapshots.

### Permissions required for the pull to work

`/opt/quartermaster` is `drwx--x---`, owned by
`quartermaster:quartermaster` — locked down since it holds the live
database. `ty` reaches `backups/` two ways, both required:

1. **Group execute on the parent**: `ty` is a member of the
   `quartermaster` group (needed for the existing deploy flow, which
   moves the redeployed binary into place). Because POSIX permission
   checks stop at the first matching category — owner, then group,
   then other — group membership with no group permission blocks
   access even when `other` would have allowed it. `chmod g+x
   /opt/quartermaster` grants traversal via group, which is the
   category that actually applies to `ty`.
2. **ACL on `backups/` itself**:
```bash
   sudo setfacl -m u:ty:rx /opt/quartermaster/backups
   sudo setfacl -d -m u:ty:rx /opt/quartermaster/backups
```
   The `-d` (default) ACL ensures every new backup file created by
   the nightly timer automatically inherits `ty`'s read access,
   without needing to re-run `setfacl` after each run.

### Off-droplet pull (systemd timer, home machine)

`/etc/systemd/system/quartermaster-backup-pull.service`:
```ini
[Unit]
Description=Pull quartermaster backups from droplet

[Service]
Type=oneshot
User=tylerl
ExecStart=/usr/bin/rsync -avz qmaster:/opt/quartermaster/backups/ /home/tylerl/quartermaster-backups/
ExecStartPost=/usr/bin/find /home/tylerl/quartermaster-backups/ -name "*.db" -mtime +90 -delete
```

Trailing slash on the source path copies the *contents* of
`backups/`, so files land directly at
`/home/tylerl/quartermaster-backups/quartermaster-YYYY-MM-DD.db`.
`ExecStartPost` only runs if the `rsync` itself succeeded, so a
failed pull never triggers cleanup of existing local snapshots.

`/etc/systemd/system/quartermaster-backup-pull.timer`:
```ini
[Unit]
Description=Run quartermaster-backup-pull daily, after the droplet's own backup

[Timer]
OnCalendar=*-*-* 02:15:00
Persistent=true

[Install]
WantedBy=timers.target
```

Enable with `sudo systemctl enable --now quartermaster-backup-pull.timer`.

**Confirmed working end to end**: droplet snapshot creates a real,
correctly-sized `.db` file; the pull retrieves it to the home
machine with a matching byte count, in the flat (non-nested) layout.

### Restore

```bash
systemctl stop quartermaster
cp <backup file> /opt/quartermaster/quartermaster.db
rm -f /opt/quartermaster/quartermaster.db-wal /opt/quartermaster/quartermaster.db-shm
systemctl start quartermaster
```
## Secret management

Four secrets, all loaded via `/etc/quartermaster.env`
(`EnvironmentFile=` in the systemd unit): `STRIPE_WEBHOOK_SECRET`,
`RESEND_API_KEY`, `STRIPE_SECRET_KEY`, and `SOFTSTORE_INTERNAL_SECRET`.
All four must **only** ever exist there — never hardcoded in source,
never committed, never placed anywhere else.

`STRIPE_SECRET_KEY` is newer than the other three (added when the
webhook started fetching line items directly from Stripe's API,
rather than relying solely on webhook payload contents) and is used
for two distinct outbound calls: fetching a session's finalized line
items for fulfillment, and fetching receipt detail (names, per-item
price, tax) for the combined email. Unlike `STRIPE_WEBHOOK_SECRET`
(which *verifies inbound* requests), this key *authenticates
outbound* calls Quartermaster makes to Stripe — a leak has different
blast radius (read access to Stripe account data via the API, not
forged webhook deliveries) and should be rotated in the Stripe
dashboard's API keys section, not the webhook signing secrets
section.

`SOFTSTORE_INTERNAL_SECRET` is shared with softstore — the identical
value must exist in both `/etc/quartermaster.env` on this droplet and
`/etc/softstore/env` (as `INTERNAL_API_SECRET`, a different variable
name for the same value) on softstore-prod. It authenticates the
WireGuard-tunneled calls between the two services in both directions
(Quartermaster looking up product codes and clearing carts on
softstore; softstore polling Quartermaster for fulfillment status).
Rotating it requires updating **both** droplets and restarting both
services — updating only one side breaks the internal API silently
(requests fail with `401` until both sides agree again).

**On 2026-07-19, this rule was violated in early development
history**: both a Resend API key and a Stripe webhook secret were
found hardcoded in old commits (`cmd/quartermaster/mail.go` and
`cmd/quartermaster/main.go` respectively), predating the current
`requireEnv`/`EnvironmentFile` pattern. GitHub's secret scanning
caught the Resend key and Resend auto-revoked it; the Stripe secret
was found by a manual history audit prompted by that alert and
disabled manually. Both were rotated, and both were scrubbed from
every commit in the repository's history using `git filter-repo`,
then force-pushed and independently verified clean against a fresh
clone from GitHub. See "Incident: secret exposure in git history"
below for the full procedure, since it will be needed again if this
ever recurs.

The current pattern (`requireEnv("STRIPE_WEBHOOK_SECRET")`,
`requireEnv("RESEND_API_KEY")`, both reading from the process
environment only) is correct and does not have this problem — this
incident was entirely contained to old, pre-refactor history.

## Webhook secret rotation

`STRIPE_WEBHOOK_SECRET` authenticates every request to
`/stripe/webhook`, set in `/etc/quartermaster.env` on the droplet.
If it's ever suspected to have leaked, rotate it:

1. In the Stripe dashboard, add a new webhook signing secret for the
   `quartermaster.<domain>/stripe/webhook` endpoint.
2. Update `STRIPE_WEBHOOK_SECRET` in `/etc/quartermaster.env` on the
   droplet.
3. `systemctl restart quartermaster`.
4. Send a test webhook from the Stripe dashboard and confirm a `200`
   and an `enqueued session` log line.
5. Revoke the old secret in the Stripe dashboard once the new one is
   confirmed working.
6. **Confirm the webhook endpoint itself is enabled in Stripe's
   dashboard** — a disabled endpoint produces no delivery attempts
   at all and can look identical to a broken secret or a dead
   service from the operator's side. This cost real debugging time
   on 2026-07-19; check it first, not last.

Rotation is cheap. If in doubt, rotate — there's no meaningful cost
to doing this preemptively versus waiting for certainty of
compromise.

## Softstore connectivity

Quartermaster and softstore communicate over a dedicated WireGuard
tunnel, `10.20.0.0/24` (`wg1` on both droplets) — separate from the
tunnel the signer droplet uses to reach the queue (`10.46.0.0/24`; `wg0` on qmaster, `wg-qmaster` on the droplet). Two tunnels
deliberately, not one: the signer and softstore are different trust
relationships with different peers, and collapsing them would give a
compromise of either peer network-level reach toward the other.

**If softstore-facing calls start failing** (product code lookups
during webhook processing, cart-clear calls, or softstore's own
status polling failing on its side):

```bash
sudo wg show wg1
```

Confirm a recent handshake (`latest handshake`) — if it's missing or
very old, the tunnel itself is down, not the application. Check
`sudo systemctl status wg-quick@wg1` on both droplets, and confirm
`ufw` still allows the internal port on `wg1` specifically
(`sudo ufw status | grep wg1`) — a firewall rule change on either
side is a more common cause of a broken tunnel than the WireGuard
interface itself failing.

If the tunnel is up but calls still fail with `401`, the shared
secret has drifted out of sync between `SOFTSTORE_INTERNAL_SECRET`
here and `INTERNAL_API_SECRET` on softstore — see "Secret
management" above.

## Monitoring webhook volume

There is currently no automated anomaly detection. The manual
baseline check:

```bash
journalctl -u quartermaster --since "1 hour ago" | grep "enqueued session"
```

No real traffic baseline exists yet — revisit once there's a few
weeks of genuine sales data, and write down what "normal" actually
looks like so a spike is recognizable rather than guessed at.

If signed-webhook volume is far above anything resembling normal
sales velocity, with no matching marketing event — rotate the
webhook secret first, investigate second. A rotation costs a few
minutes; an ongoing leak does not.

## Incident: signing key exposure

If `signing.key` is ever exposed (committed to git, copied to the
wrong machine, leaked any other way):

1. There is no way to "revoke" already-issued licenses signed with
   this key — they will continue to verify forever, by design (see
   `docs/license-scheme.md`).
2. Generate a new keypair immediately (`cmd/keygen`).
3. Follow "Rotating the key" above.
4. Update the droplet and ship a client update per that procedure.
5. All *future* licenses are signed with the new key. Every license
   already issued, under the old key, keeps working, because the old
   key remains in the embedded list on both the droplet and client
   apps — unless no real licenses had been issued yet, in which case
   a clean replacement with no old-key preservation is appropriate.

## Incident: signing key lost (not exposed — simply missing)

Distinct from exposure: `signing.key` is gitignored by design and is
never carried along by git-based tooling — a fresh clone, a
directory rename, or a history rewrite that swaps the working
directory will leave it behind. If the signer fails to start with
`loading signing key: open signing.key: no such file or directory`:

1. Check whether the key exists in a previous/renamed version of the
   working directory before assuming it's gone.
2. If truly lost and no real licenses have been issued yet under it,
   treat this the same as a routine rotation: generate a fresh
   keypair, deploy the new public key, done — no compatibility
   concerns.
3. If real licenses *have* been issued under the lost key, this
   becomes equivalent to a key exposure incident in terms of
   procedure, except the risk is unavailability of the old key for
   future verification rather than forgery — the old key must be
   sourced from a backup (if one exists) or those licenses will need
   an alternative verification path. This is exactly why both signing keys are backed up in KeePassXC
   (separate from this repo's git history, and never via DO
   snapshots), and why that backup should be tested periodically.

## Incident: secret exposure in git history

If a real secret (API key, webhook secret, etc.) is found hardcoded
in any commit, past or present:

1. **Rotate or revoke the secret at its source immediately** — this
   is the actual time-sensitive step. A secret sitting in git
   history is not neutralized just by removing it from the current
   file; the old commit still has it until history is rewritten, and
   rewriting takes real time to do safely. Kill the credential first.
2. Identify every commit containing it:
```bash
   git log --all --oneline -S"<the secret string>"
```
3. Make a full local backup of the repo before doing anything
   destructive:
```bash
   cp -r quartermaster quartermaster-backup-before-scrub
```
4. Clone fresh with `--no-local` (required — `git filter-repo`
   refuses to run against a repo that shares local object storage
   with another):
```bash
   git clone --no-local quartermaster quartermaster-scrub
   cd quartermaster-scrub
```
5. Rewrite history:
```bash
   git filter-repo --replace-text <(echo '<secret>==>REDACTED')
```
6. Verify locally:
```bash
   git log --all -p | grep "<secret>"
```
   Must be empty.
7. Force-push:
```bash
   git remote add origin https://github.com/laudendev/quartermaster.git
   git push origin --force --all
   git push origin --force --tags
```
8. **Verify against a completely fresh clone from GitHub itself**,
   not just local state — this is the only real proof of what's
   actually published:
```bash
   cd /tmp
   git clone https://github.com/laudendev/quartermaster.git verify
   cd verify
   git log --all -p | grep "<secret>"
```
   Must be empty.
9. Check whether any tag or GitHub Release was affected. If a
   release's `target_commitish` is a branch name (e.g. `master`)
   rather than a fixed commit, it survives a force-push
   automatically — confirm with:
```bash
   gh api repos/laudendev/quartermaster/releases/tags/<tag> --jq .target_commitish
```
   If pinned to a commit hash instead, the release will need to be
   deleted and recreated against the new hash.
10. **Swap the working directory over to the cleaned clone** and
    remember to manually copy `signing.key`/`signing.pub` into it —
    see "Incident: signing key lost" above, since this exact swap
    caused that incident on 2026-07-19.
11. Delete old backup directories once fully confident the scrub is
    solid — they still contain the exposed secret in local history.

This procedure was used twice on 2026-07-19, for a Resend API key
and a Stripe webhook secret, both found hardcoded in commits
predating the current `requireEnv`-based secret handling. Both were
successfully scrubbed and verified clean against a fresh clone from
`https://github.com/laudendev/quartermaster`.

## Observed in practice

On 2026-07-19, during the git history scrub above, a directory swap
(`mv quartermaster quartermaster-old`, `mv quartermaster-scrub
quartermaster`) left `signing.key` and `signing.pub` behind in the
renamed old directory, since they are gitignored and never travel
with any git operation. The signer crash-looped for several hours
(`loading signing key: open signing.key: no such file or directory`)
before this was noticed and a fresh keypair was generated to replace
the lost one — an acceptable resolution since no real license had
yet been issued.

During the entire outage, `quartermaster` continued operating
normally: a test webhook was accepted, verified, and enqueued while
the signer was completely down. Nothing was lost, nothing errored on
the customer-facing side. The moment the signer was restored (with a
new key), it immediately picked up the queued request, signed it,
and the resulting license email was delivered — with no manual
intervention beyond fixing the signer itself.

This is the split-key architecture's central claim — that a signer
outage never becomes a quartermaster outage — observed under a real,
unplanned fault rather than only reasoned about in the abstract.

## Support: customer never received their license email

One purchase produces one combined receipt email covering every
product in that session, not one email per license — see
`docs/api.md`'s "Combined receipt." Every license key is stored in
its `sign_requests` row regardless of whether that email ever went
out, so the keys themselves are never actually lost; what's worth
checking is whether the *email* was sent, retried, or is stuck.

**Find every license key for a session or customer:**

```bash
sqlite3 /opt/quartermaster/quartermaster.db \
  "SELECT session_id, product, license_key, status FROM sign_requests WHERE email = 'customer@example.com' ORDER BY created_at;"
```

If any row shows `status != 'signed'`, that item genuinely isn't
ready yet — the signer hasn't picked it up, or is down (see
"Observed in practice" below for what a signer outage looks like;
Quartermaster keeps queuing normally, the item just waits).

**Check whether the session's receipt email actually sent:**

```bash
sqlite3 /opt/quartermaster/quartermaster.db \
  "SELECT * FROM session_emails WHERE session_id = '<session_id from above>';"
```

- No row at all: the session either isn't fully signed yet (every
  item must be `signed` before an email is attempted at all — see
  `docs/api.md`), or the automatic retry loop simply hasn't run
  since it became ready. Wait for the next retry pass (`runEmailRetryLoop`,
  interval set in `cmd/quartermaster/main.go`) or restart the service
  to force an immediate check.
- `email_sent = 0`, `email_attempts > 0`: delivery has failed at
  least once (bad address, Resend outage) and will keep auto-retrying
  up to `maxEmailAttempts` (currently 5, in `email_retry.go`). Above
  that cap, retries stop permanently and this becomes a manual
  support action.
- `email_sent = 1`: the email genuinely went out. A "never received"
  report at this point usually means spam filtering or a mistyped
  address, not a system failure — verify the address on the Stripe
  side of the transaction before assuming anything is broken here.

**Manual resend:** there is no built-in resend command. Relay the
license keys from the first query directly to the customer, or
manually re-trigger `sendSessionReceiptEmail` for that session via a
one-off script if a properly-formatted receipt (not just raw keys)
matters for the interaction.

## Support: customer needs to reactivate after an OS reinstall

Per `docs/license-scheme.md`, an OS reinstall produces a new
fingerprint — there is no self-service reset. After verifying the
purchase:

```bash
sqlite3 /opt/quartermaster/quartermaster.db \
  "DELETE FROM activations WHERE license_id = '<hex license id>' AND fingerprint = '<old fingerprint>';"
```

If the customer's old fingerprint isn't known, deleting all
activation rows for that `license_id` is an acceptable fallback — it
temporarily frees every seat on the license, not just the stale one,
but is simple and low-risk given this is a rare, human-verified
support action.
