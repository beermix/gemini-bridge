#!/usr/bin/env bash
set -e

echo "=== Building gemini-bridge binaries ==="
mkdir -p bin

echo "[1/3] Building Linux AMD64 (bin/gemini-bridge-linux-amd64)..."
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o bin/gemini-bridge-linux-amd64 ./cmd/gemini-bridge

echo "[2/3] Building Linux ARM64 (bin/gemini-bridge-linux-arm64)..."
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o bin/gemini-bridge-linux-arm64 ./cmd/gemini-bridge

echo "[3/3] Building Host binary (bin/gemini-bridge)..."
go build -ldflags="-s -w" -o bin/gemini-bridge ./cmd/gemini-bridge

echo "=== Build complete! Binaries located in bin/ ==="
ls -lh bin/
