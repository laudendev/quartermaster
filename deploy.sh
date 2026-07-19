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

echo "Building signer..."
go build -ldflags="-s -w" -o signer ./cmd/signer

echo "Deploying signer (local)..."
sudo systemctl stop signer
sudo systemctl start signer

echo "Verifying signer..."
systemctl is-active signer && systemctl show signer -p ActiveEnterTimestamp

echo "Done."
