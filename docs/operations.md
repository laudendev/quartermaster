# Operations

Deploy steps, key ceremony, backups, secret rotation, and incident
response. This is the doc for "something is on fire, what do I do."

## Deploy

`./deploy.sh`, run from the repo root on the signer machine
(`/home/tylerl/quartermaster`) — deliberately run from here, not the
droplet, since this is also where `signing.key` lives and the script
needs local access to build and restart the signer.

1. Runs the full test suite (`go test ./...`). Aborts on any
   failure — nothing broken ships.
2. Cross-compiles `quartermaster` for the droplet
   (`CGO_ENABLED=0 GOOS=linux GOARCH=amd64`), `scp`s it to a staging
   directory, and an `inotifywait`-based watcher on the droplet
   detects the new binary and redeploys automatically. Verifies the
   service is active afterward.
3. Builds `signer` locally (plain `go build`, no cross-compilation —
   it already targets the machine it runs on) directly into
   `/home/tylerl/quartermaster/signer`, the exact path the systemd
   unit's `ExecStart` expects — build output and deploy target are
   the same file, so no copy step is needed.
4. Stops and restarts the `signer` systemd service (`sudo systemctl
   stop/start signer`) so the new binary takes effect, then verifies
   it's active.

Both binaries are deployed by one script, one run, from one machine.
There is no separate signer deploy process.

**`deploy.sh` does not copy `signing.pub` to the droplet.** If the
signing key has been rotated, `signing.pub` must be copied to the
droplet and `quartermaster` restarted separately — see "Rotating
the key" below.

Confirmed working end to end: full redeploy plus a live test Stripe
checkout.

## Key ceremony

The signing keypair (`signing.key`, `signing.pub`) is generated on
the signer machine, using `cmd/keygen`:

```bash
cd cmd/keygen
go run .
```

This writes `signing.key` (mode `0600`) and `signing.pub` (mode
`0644`) to the current directory. Move both into the repo root
(`~/quartermaster/`), where the signer's systemd unit expects them
(`WorkingDirectory=/home/tylerl/quartermaster`).

**`signing.key` never leaves the signer machine, ever, under any
circumstance.** It is not committed to git (`.gitignore` enforces
this), not backed up to any third-party service, not copied to the
droplet, not emailed, not pasted into a chat tool. If this file is
ever exposed, treat it as a full compromise — see "Incident: signing
key exposure" below.

**`signing.key` is also not preserved by any git-based backup or
history-rewrite tooling** — it is gitignored by design, so a fresh
clone, a history scrub, or a directory reorganization will never
carry it along. If the working directory is ever renamed, moved, or
replaced with a fresh clone, `signing.key` and `signing.pub` must be
manually copied into the new location, or the signer will fail to
start (`loading signing key: open signing.key: no such file or
directory`). This exact failure occurred on 2026-07-19 during a git
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

1. Generate a new keypair (`cmd/keygen`), on the signer machine.
2. If real licenses exist under the current key, add the new key to
   a rotation history table (promote the single-row table above into
   a full history) and append it to `activationAPI.pubs` in
   `cmd/quartermaster/main.go` — keep the old key too, never remove
   a historical key once real licenses depend on it. If no real
   licenses exist yet, a clean single-key replacement is fine, as
   was done on 2026-07-19.
3. Copy `signing.pub` to the droplet (`deploy.sh` does not do this
   automatically):
```bash
   scp signing.pub ty@qmaster:/opt/quartermaster/signing.pub
   ssh qmaster "sudo systemctl restart quartermaster"
```
4. Ship a client app update that adds the new public key to its own
   embedded list, alongside every previous key (only relevant once
   real clients exist and real licenses have been issued under a
   prior key).
5. The signer now signs new licenses with the new key.

## Backups

`activations` and `sign_requests` are the business — losing this
database means losing the record of who has activated what, and any
license keys not yet delivered.

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

Two secrets, both loaded via `/etc/quartermaster.env`
(`EnvironmentFile=` in the systemd unit): `STRIPE_WEBHOOK_SECRET` and
`RESEND_API_KEY`. Both must **only** ever exist there — never
hardcoded in source, never committed, never placed anywhere else.

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
   an alternative verification path. This is exactly why a
   `signing.key` backup strategy (separate from this repo's git
   history, per its "never leaves the signer machine" rule) should
   be revisited before real licenses are issued at volume.

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

The license key is stored in the `sign_requests` row regardless of
whether the email send succeeded (see `docs/api.md`). Look up by
Stripe session ID or customer email:

```bash
sqlite3 /opt/quartermaster/quartermaster.db \
  "SELECT license_key FROM sign_requests WHERE email = 'customer@example.com' AND status = 'signed';"
```

Resend manually, or relay the key directly. There is no automatic
retry, because retrying delivery to a wrong or mistyped address
wouldn't help — the fix is always a manual lookup and resend.

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
