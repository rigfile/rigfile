#!/bin/sh
# Rigfile installer for macOS and Linux.
#
#   curl -fsSL https://github.com/digitaldreamer3462/rigfile/releases/latest/download/install.sh | sh
#
# It downloads a release, verifies the minisign signature of SHA256SUMS with the public key below, checks the
# archive's SHA-256 against that signed list, and only then installs the binary. Nothing is run before verification.
# The public key is written into this file when the release is built; a copy of the script without a key refuses to
# install. Requires: curl (or wget), tar, sha256sum or shasum, and minisign (brew install minisign / apt install
# minisign / dnf install minisign).
#
# Environment: RIGFILE_VERSION (default: latest), RIGFILE_INSTALL_DIR (default: ~/.local/bin),
#              RIGFILE_BASE_URL (default: the GitHub releases; tests use file://).
#              RIGFILE_INSECURE_SKIP_SIGNATURE=1 skips ONLY the signature check (the SHA-256 check still runs); use it
#              only if you cannot install minisign, and know that then nothing proves who built the download.
set -eu

REPO="__RIGFILE_REPO__"
PUBKEY="__RIGFILE_PUBKEY__"
BASE="${RIGFILE_BASE_URL:-https://github.com/$REPO/releases/download}"

die() { echo "rigfile install: $*" >&2; exit 1; }

case "$PUBKEY" in ""|__RIGFILE_*) [ "${RIGFILE_INSECURE_SKIP_SIGNATURE:-}" = 1 ] || die "this copy of the installer has no signing key built in; use the install.sh attached to a release" ;; esac

fetch() { # fetch URL DEST
  if command -v curl >/dev/null 2>&1; then curl -fsSL --proto '=https,file' --max-filesize 209715200 -o "$2" "$1"
  elif command -v wget >/dev/null 2>&1; then wget -q -O "$2" "$1"
  else die "curl or wget is required"; fi
}
sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1
  elif command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | cut -d' ' -f1
  else die "sha256sum or shasum is required"; fi
}

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in linux|darwin) ;; *) die "unsupported operating system $os (Windows: use install.ps1)" ;; esac
case "$(uname -m)" in x86_64|amd64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; *) die "unsupported CPU $(uname -m)" ;; esac

tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT INT TERM

version="${RIGFILE_VERSION:-}"
if [ -z "$version" ]; then
  fetch "https://api.github.com/repos/$REPO/releases/latest" "$tmp/latest.json" || die "cannot find the latest release"
  version=$(sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"v\{0,1\}\([^"]*\)".*/\1/p' "$tmp/latest.json" | head -n 1)
  [ -n "$version" ] || die "cannot read the latest version"
fi
version=${version#v}
case "$version" in *[!0-9A-Za-z._-]*|"") die "invalid version '$version'" ;; esac
tag="v$version"
archive="rigfile_${version}_${os}_${arch}.tar.gz"

echo "Installing rigfile $version for $os/$arch"
fetch "$BASE/$tag/SHA256SUMS" "$tmp/SHA256SUMS" || die "cannot download SHA256SUMS"

if [ "${RIGFILE_INSECURE_SKIP_SIGNATURE:-}" = 1 ]; then
  echo "WARNING: skipping the signature check; only the SHA-256 is verified" >&2
else
  command -v minisign >/dev/null 2>&1 || die "minisign is required to verify the download (brew install minisign | apt install minisign | dnf install minisign). Or set RIGFILE_INSECURE_SKIP_SIGNATURE=1 to skip only the signature check."
  fetch "$BASE/$tag/SHA256SUMS.minisig" "$tmp/SHA256SUMS.minisig" || die "the release has no signature (SHA256SUMS.minisig); refusing"
  out=$(minisign -V -P "$PUBKEY" -m "$tmp/SHA256SUMS" -x "$tmp/SHA256SUMS.minisig" 2>&1) || die "signature check FAILED; refusing to install: $out"
  # the signed trusted comment names the release: an old signed list cannot be replayed under a newer tag
  echo "$out" | grep -F "rigfile $tag" >/dev/null 2>&1 || die "the signed checksums are not for $tag; refusing"
  echo "signature verified"
fi

want=$(awk -v f="$archive" '$2==f || $2=="*" f {print $1}' "$tmp/SHA256SUMS" | head -n 1)
[ -n "$want" ] || die "$tag has no build for $os/$arch"
fetch "$BASE/$tag/$archive" "$tmp/$archive" || die "cannot download $archive"
got=$(sha256_of "$tmp/$archive")
[ "$got" = "$want" ] || die "checksum mismatch for $archive; refusing"
echo "checksum verified"

mkdir "$tmp/x"
tar -xzf "$tmp/$archive" -C "$tmp/x" || die "cannot unpack $archive"
bin=$(find "$tmp/x" -type f -name rigfile | head -n 1)
[ -n "$bin" ] || die "$archive does not contain rigfile"

dest="${RIGFILE_INSTALL_DIR:-$HOME/.local/bin}"
mkdir -p "$dest"
cp "$bin" "$dest/rigfile.new" && chmod 755 "$dest/rigfile.new" && mv -f "$dest/rigfile.new" "$dest/rigfile" || die "cannot write to $dest"
echo "installed $dest/rigfile"
case ":$PATH:" in *":$dest:"*) ;; *) echo "note: $dest is not on your PATH; add it to your shell profile" ;; esac
"$dest/rigfile" version
