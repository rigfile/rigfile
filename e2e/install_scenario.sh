#!/bin/sh
# Runs INSIDE the container as an unprivileged user with the real minisign tool.
set -eu
fail() { echo "INSTALL E2E FAIL: $*" >&2; exit 1; }
V=9.9.9
mkdir -p /tmp/rel/v$V && cp /release-src/* /tmp/rel/v$V/
cd /tmp/rel/v$V

minisign -G -W -p /tmp/k.pub -s /tmp/k.sec >/dev/null
minisign -S -s /tmp/k.sec -m SHA256SUMS -x SHA256SUMS.minisig -t "rigfile v$V" >/dev/null
PUB=$(tail -n 1 /tmp/k.pub)
sed "s|__RIGFILE_PUBKEY__|$PUB|; s|__RIGFILE_REPO__|example/rigfile|" /install.sh.tmpl > /tmp/install.sh

run() { RIGFILE_VERSION=$V RIGFILE_BASE_URL=file:///tmp/rel RIGFILE_INSTALL_DIR=/tmp/bin sh /tmp/install.sh; }

echo "== the Go verifier agrees with the real minisign tool"
ARCH=$(uname -m); case "$ARCH" in x86_64) A=amd64 ;; *) A=arm64 ;; esac
tar -xzf rigfile_${V}_linux_${A}.tar.gz -C /tmp
RF=/tmp/rigfile_${V}_linux_${A}/rigfile
"$RF" verify-signature SHA256SUMS --pubkey /tmp/k.pub | grep -q "signature is valid" || fail "rigfile verify-signature disagrees with minisign on a genuine signature"
cp SHA256SUMS /tmp/tampered && echo x >> /tmp/tampered && cp SHA256SUMS.minisig /tmp/tampered.minisig
if "$RF" verify-signature /tmp/tampered --pubkey /tmp/k.pub 2>/dev/null; then fail "tampered file accepted by rigfile"; fi

echo "== install (genuine release)"
run > /tmp/out.txt 2>&1 || { cat /tmp/out.txt >&2; fail "installer failed on a genuine release"; }
grep -q "signature verified" /tmp/out.txt && grep -q "checksum verified" /tmp/out.txt || { cat /tmp/out.txt >&2; fail "verification steps missing"; }
[ "$(/tmp/bin/rigfile version)" = "rigfile $V" ] || fail "installed binary does not report $V"
rm -rf /tmp/bin

echo "== tampered archive is refused"
cp rigfile_${V}_linux_${A}.tar.gz /tmp/orig.tgz
echo junk >> rigfile_${V}_linux_${A}.tar.gz
if run > /tmp/out.txt 2>&1; then fail "a tampered archive was installed"; fi
grep -q "checksum mismatch" /tmp/out.txt || { cat /tmp/out.txt >&2; fail "wrong refusal reason"; }
[ ! -e /tmp/bin/rigfile ] || fail "binary present after a refused install"
cp /tmp/orig.tgz rigfile_${V}_linux_${A}.tar.gz

echo "== edited checksums are refused (signature no longer matches)"
cp SHA256SUMS /tmp/sums.orig; echo "0000000000000000000000000000000000000000000000000000000000000000  extra" >> SHA256SUMS
if run > /tmp/out.txt 2>&1; then fail "edited checksums were accepted"; fi
grep -q "signature check FAILED" /tmp/out.txt || { cat /tmp/out.txt >&2; fail "wrong refusal reason"; }
cp /tmp/sums.orig SHA256SUMS

echo "== a different signing key is refused"
minisign -G -W -f -p /tmp/k2.pub -s /tmp/k2.sec >/dev/null
minisign -S -s /tmp/k2.sec -m SHA256SUMS -x SHA256SUMS.minisig -t "rigfile v$V" >/dev/null
if run > /tmp/out.txt 2>&1; then fail "a signature from another key was accepted"; fi
minisign -S -s /tmp/k.sec -m SHA256SUMS -x SHA256SUMS.minisig -t "rigfile v$V" >/dev/null

echo "== signed checksums for another version are refused (replay)"
minisign -S -s /tmp/k.sec -m SHA256SUMS -x SHA256SUMS.minisig -t "rigfile v1.0.0" >/dev/null
if run > /tmp/out.txt 2>&1; then fail "a replayed signature was accepted"; fi
grep -q "not for v$V" /tmp/out.txt || { cat /tmp/out.txt >&2; fail "wrong refusal reason"; }
minisign -S -s /tmp/k.sec -m SHA256SUMS -x SHA256SUMS.minisig -t "rigfile v$V" >/dev/null

echo "== a release without a signature is refused"
mv SHA256SUMS.minisig /tmp/sig.bak
if run > /tmp/out.txt 2>&1; then fail "an unsigned release was installed"; fi
mv /tmp/sig.bak SHA256SUMS.minisig

echo "== the key-less template refuses to run"
if RIGFILE_VERSION=$V RIGFILE_BASE_URL=file:///tmp/rel RIGFILE_INSTALL_DIR=/tmp/bin2 sh /install.sh.tmpl > /tmp/out.txt 2>&1; then fail "the template without a key ran"; fi
grep -q "no signing key" /tmp/out.txt || fail "wrong refusal reason for the key-less template"
echo "INSTALL E2E OK"
