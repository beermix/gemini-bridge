@echo off
setlocal enabledelayedexpansion

echo === Building gemini-bridge binaries ===
if not exist "bin" mkdir bin

echo [1/3] Building Linux AMD64 (bin\gemini-bridge-linux-amd64)...
set CGO_ENABLED=0
set GOOS=linux
set GOARCH=amd64
go build -ldflags="-s -w" -o bin\gemini-bridge-linux-amd64 .\cmd\gemini-bridge
if errorlevel 1 (
    echo [ERROR] Failed to build Linux AMD64 binary
    exit /b 1
)

echo [2/3] Building Linux ARM64 (bin\gemini-bridge-linux-arm64)...
set CGO_ENABLED=0
set GOOS=linux
set GOARCH=arm64
go build -ldflags="-s -w" -o bin\gemini-bridge-linux-arm64 .\cmd\gemini-bridge
if errorlevel 1 (
    echo [ERROR] Failed to build Linux ARM64 binary
    exit /b 1
)

echo [3/3] Building Windows Executable (bin\gemini-bridge.exe)...
set CGO_ENABLED=0
set GOOS=windows
set GOARCH=amd64
go build -ldflags="-s -w" -o bin\gemini-bridge.exe .\cmd\gemini-bridge
if errorlevel 1 (
    echo [ERROR] Failed to build Windows binary
    exit /b 1
)

echo.
echo === Build complete! Binaries created in bin\ ===
dir bin
