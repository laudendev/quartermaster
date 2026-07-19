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
`0644`) to the current directory.

**`signing.key` never leaves the signer machine, ever, under any
circumstance.** It is not committed to git (`.gitignore` enforces
this), not backed up to any third-party service, not copied to the
droplet, not emailed, not pasted into a chat tool. If this file is
ever exposed, treat it as a full compromise — see "Incident: signing
key exposure" below.

`signing.pub` is not sensitive. It needs to exist in three places:
- On the droplet, loaded by `quartermaster` at startup
  (`activation.go`'s `activationAPI.pubs`) to verify licenses during
  activation.
- Embedded in every customer-facing application, to verify licenses
  fully offline.
- Recorded in this document's rotation history (below), so every
  public key that has ever been live is permanently on record.

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

### Rotation history

Record every key that has ever been live here, so the full list a
client should embed is always known from this document alone.

| Public key (hex) | Live from | Live until | Reason |
|---|---|---|---|
| `<TY: paste the output of signing.pub here>` | `<issue date>` | current | Initial key |

### Rotating the key

1. Generate a new keypair (`cmd/keygen`), on the signer machine.
2. Add the new public key to the rotation history table above.
3. In `cmd/quartermaster/main.go`, append the new key to the slice
   passed into `activationAPI{pubs: ...}`. Keep the old key in the
   slice — never remove a historical key. Redeploy.
4. Ship a client app update that adds the new public key to its own
   embedded list, alongside every previous key.
5. The signer now signs new licenses with the new key. Old licenses,
   already in customers' hands, still verify because the old public
   key is still present in both the droplet's and the client's
   embedded lists.

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

## Webhook secret rotation

`STRIPE_WEBHOOK_SECRET` authenticates every request to
`/stripe/webhook`, set in `/etc/quartermaster.env` on the droplet
(loaded via the unit's `EnvironmentFile=`). If it's ever suspected
to have leaked (see "Incident: webhook secret leak" below), rotate
it:

1. In the Stripe dashboard, add a new webhook signing secret for the
   `quartermaster.<domain>/stripe/webhook` endpoint.
2. Update `STRIPE_WEBHOOK_SECRET` in `/etc/quartermaster.env` on the
   droplet.
3. `systemctl restart quartermaster`.
4. Send a test webhook from the Stripe dashboard and confirm a `200`
   in the logs.
5. Revoke the old secret in the Stripe dashboard once the new one is
   confirmed working.

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
3. Follow "Rotating the key" above — this is a fully-supported,
   tested procedure.
4. Update the droplet and ship a client update per that procedure.
5. All *future* licenses are signed with the new key. Every license
   already issued, under the old key, keeps working, because the old
   key remains in the embedded list on both the droplet and client
   apps.

## Incident: webhook secret leak

See "Webhook secret rotation" above — rotate immediately, don't wait
for confirmation.

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
