#!/usr/bin/env bash
set -euo pipefail
export GOTOOLCHAIN=go1.27.0
export GOEXPERIMENT=simd
export GOWORK=/devhoros/c2simd/go.work
ROOT="$(cd "$(dirname "$0")/../../../.." && pwd)"
C2SIMD="$(cd "$(dirname "$0")/../../.." && pwd)"
PKG="$(cd "$(dirname "$0")/../.." && pwd)"

echo "== strate 1 : cue vet =="
(cd "$C2SIMD/sgoiter/spec" && cue vet . && cue vet ./findings)
(cd "$PKG/c2db/spec" && cue vet .)

echo "== strate 2 : go test -race ./... =="
(cd "$PKG" && go test -race -count=1 ./...)

echo "== strate 3 : repli scalaire (sans GOEXPERIMENT=simd) =="
(cd "$PKG" && env -u GOEXPERIMENT GOTOOLCHAIN=go1.27.0 GOWORK="$GOWORK" go test -count=1 ./c2db)

echo "== strate 4 : provenance =="
(cd "$C2SIMD" && GOTOOLCHAIN=go1.27.0 GOEXPERIMENT=simd GOWORK="$GOWORK" go test -count=1 ./sgoiter/provenance)
if command -v c2simd-fyne-guard >/dev/null 2>&1; then
  c2simd-fyne-guard verify-provenance --root "$PKG/c2db"
elif [[ -x "$C2SIMD/bin/c2simd-fyne-guard" ]]; then
  "$C2SIMD/bin/c2simd-fyne-guard" verify-provenance --root "$PKG/c2db"
else
  echo "  skip c2simd-fyne-guard (absent)"
fi

echo "== strate 5 : Poly1305 faute puis reprise (dans strate 2) =="

echo "== strate 6 : CI profonde cigate55/cihook55 =="
(cd "$PKG" && go test -race -count=1 ./c2db/ci)

echo "OK ci_check c2db"
