#!/bin/bash
set -euo pipefail

echo "Testing..."
go test ./...

echo "Building..."
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o quartermaster-linux ./cmd/quartermaster

echo "Deploying..."
scp quartermaster-linux ty@qmaster:/opt/quartermaster-deploy/

echo "Waiting for redeploy..."
sleep 3

echo "Verifying..."
ssh qmaster "systemctl is-active quartermaster && systemctl show quartermaster -p ActiveEnterTimestamp"

echo "Done."
