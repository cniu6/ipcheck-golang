#!/usr/bin/env bash
set -euo pipefail

DIST=dist
APP=ipcheck
mkdir -p "$DIST"

# Colors
GREEN='\033[0;92m'
RED='\033[0;91m'
RESET='\033[0m'

# Version (optional)
GIT_SHA=$(git rev-parse --short HEAD 2>/dev/null || echo nogit)
LDFLAGS="-s -w -X main.buildSHA=$GIT_SHA"
export CGO_ENABLED=0

# Matrix
TARGETS=(
  "windows/amd64" "windows/arm64" "windows/386"
  "linux/amd64" "linux/arm64" "linux/386" "linux/mips" "linux/mipsle" "linux/mips64" "linux/mips64le" "linux/ppc64le" "linux/s390x"
  "darwin/amd64" "darwin/arm64"
  "freebsd/amd64" "freebsd/arm64" "freebsd/386"
  "openbsd/amd64" "openbsd/arm64" "openbsd/386"
  "netbsd/amd64" "netbsd/arm64" "netbsd/386"
)

FAIL=0
for t in "${TARGETS[@]}"; do
  GOOS=${t%%/*}
  GOARCH=${t##*/}
  EXT=""
  if [[ "$GOOS" == "windows" ]]; then EXT=".exe"; fi
  OUT="$DIST/$APP-$GOOS-$GOARCH$EXT"
  echo "Building $OUT"
  if GOOS=$GOOS GOARCH=$GOARCH go build -trimpath -ldflags "$LDFLAGS" -o "$OUT" .; then
    echo -e "${GREEN}[Success]${RESET} $OUT"
  else
    echo -e "${RED}[Error]${RESET} Build failed for $GOOS/$GOARCH"
    FAIL=1
  fi
done

if [[ $FAIL -eq 0 ]]; then
  echo -e "${GREEN}All builds completed successfully.${RESET}"
else
  echo -e "${RED}Some builds failed.${RESET}"
  exit 1
fi 