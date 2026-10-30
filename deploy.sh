#!/bin/bash
set -euo pipefail

echo "Testing..."
go test ./...

echo "Building quartermaster..."
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o quartermaster-linux ./cmd/quartermaster

echo "Deploying quartermaster..."
scp quartermaster-linux ty@qmaster:/opt/quartermaster-deploy/

echo "Waiting for redeploy..."
sleep 3

echo "Verifying quartermaster..."
ssh qmaster "systemctl is-active quartermaster && systemctl show quartermaster -p ActiveEnterTimestamp"

echo "Building signer (static, for the signer droplet)..."
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o signer-linux ./cmd/signer

echo "Deploying signer to qm-signer over the WireGuard admin tunnel..."
sum=$(sha256sum signer-linux | cut -d' ' -f1)
scp signer-linux qm-signer:/tmp/qm-signer.new
printf '%s  /tmp/qm-signer.new\n' "$sum" | ssh qm-signer 'sha256sum -c -'
ssh -t qm-signer 'sudo install -m 755 -o root -g root /tmp/qm-signer.new /usr/local/bin/qm-signer && rm -f /tmp/qm-signer.new && sudo systemctl restart qm-signer'

echo "Verifying signer..."
sleep 3
ssh qm-signer 'systemctl is-active qm-signer && systemctl show qm-signer -p ActiveEnterTimestamp'

echo "Done."
