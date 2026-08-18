#!/bin/bash
#
# Signs one freshly built binary with the Developer ID. goreleaser calls this
# as a post-build hook, once per binary, before the archives are assembled.
#
# The disk image never needed this: make-app.sh signs every executable it
# bundles. The tar.gz archives had no equivalent, so v0.1.0 shipped four
# ad-hoc signed binaries. `codesign --verify --strict` passed on all of them,
# because an ad-hoc signature is a real signature, but there was no identity
# behind it.
#
# That matters more than it sounds. `goguma install` runs codesign against the
# helper before copying it into place as root, and prints who signed it.
# SECURITY.md offers that line as the one part of the output a tampered copy
# could not produce, because it is macOS reading the bytes rather than goguma
# describing itself. Against an ad-hoc binary it printed nothing worth reading,
# and the claim was true only for people who installed the app.
#
# usage: sign-binary.sh <path>
set -euo pipefail

BIN="${1:?usage: sign-binary.sh <path>}"

# No certificate is a supported state, exactly as in make-app.sh: a fork
# building its own tag has none, and must still get a release out. The archives
# are then ad-hoc signed, which is where this started, so nothing regresses.
[ -n "${SIGN_ID:-}" ] || exit 0

# The same build matrix produces Linux binaries. codesign cannot sign those and
# there is nothing wrong with them, so they are skipped rather than failed on.
case "$(file -b "$BIN")" in
    *Mach-O*) ;;
    *) exit 0 ;;
esac

# --options runtime: the hardened runtime, which notarization requires. Signing
#   without it forecloses notarizing these archives later without re-signing.
# --timestamp: so the signature still verifies after the certificate expires,
#   rather than every old release going bad on one date in the future.
codesign --force --options runtime --timestamp --sign "$SIGN_ID" "$BIN"

# Proves it, rather than assuming the command above did what it said.
codesign --verify --strict "$BIN"
