#!/bin/sh
# Container E2E: for each distro, start from a fresh image, apply the e2e rig as an unprivileged user
# and check the result. Usage: e2e/run.sh [ubuntu|fedora|arch ...]   (default: ubuntu fedora)
# macOS is not covered here: see e2e/README.md for the owner-run Tart procedure.
#
# arch (pacman) is NOT in the default set: archlinux has no official arm64 image, so on an Apple Silicon host
# it only runs under QEMU's amd64 emulation, which is unreliable for this Go binary -- found live, 2026-09-27:
# 2 of 5 runs crashed non-deterministically inside regexp/syntax at DIFFERENT lines each time (a QEMU binary
# translation bug, not a Rigfile one: the exact same binary ran clean, repeatedly, natively on arm64, and a
# minimal regexp.MustCompile repro of the same pattern never crashed under the same emulation). Pass `arch`
# explicitly on x86_64 hardware, or once a native arm64 archlinux image exists; pacman support otherwise stays
# verified only from catalog/tools.yaml (code review, not an end-to-end run).
set -eu
cd "$(dirname "$0")/.."
distros="${*:-ubuntu fedora}"
hostarch="$(docker info --format '{{.Architecture}}')"
case "$hostarch" in x86_64) hostgoarch=amd64 ;; aarch64|arm64) hostgoarch=arm64 ;; *) echo "unsupported docker arch $hostarch" >&2; exit 1 ;; esac

# archlinux:latest is x86_64 only (Arch has no official arm64 build; Arch Linux ARM is a separate, unofficial
# project); every other distro here runs at the host's native architecture.
goarch_for() {
  case "$1" in arch) echo amd64 ;; *) echo "$hostgoarch" ;; esac
}

status=0
for d in $distros; do
  ga="$(goarch_for "$d")"
  case "$ga" in amd64) dockerplat=linux/amd64 ;; arm64) dockerplat=linux/arm64 ;; esac
  bin="e2e/build/$ga/rigfile"
  if [ ! -x "$bin" ]; then
    mkdir -p "e2e/build/$ga"
    CGO_ENABLED=0 GOOS=linux GOARCH="$ga" go build -o "$bin" ./cmd/rigfile
  fi
  echo "=== $d ($ga$( [ "$ga" != "$hostgoarch" ] && echo ", emulated" ))"
  cp "$bin" e2e/build/rigfile
  docker build -q --platform "$dockerplat" -f "e2e/Dockerfile.$d" -t "rigfile-e2e-$d" e2e >/dev/null
  if docker run --rm --platform "$dockerplat" "rigfile-e2e-$d"; then echo "=== $d: PASS"; else echo "=== $d: FAIL"; status=1; fi
done
exit $status
