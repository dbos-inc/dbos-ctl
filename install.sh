#!/bin/sh
# Install dbosctl, the DBOS Conductor CLI.
#
#   curl -sSfL https://raw.githubusercontent.com/dbos-inc/dbos-ctl/main/install.sh | sh
#
# Environment:
#   VERSION   release tag to install (default: the latest release)
#   BIN_DIR   where to put the binary (default: the first writable of
#             /usr/local/bin, $HOME/.local/bin, then the current directory)
#
# POSIX sh, so it runs under dash and busybox ash as well as bash. Needs curl or
# wget, and tar.

set -eu

REPO=dbos-inc/dbos-ctl
BINARY=dbosctl

die() { printf 'install: %s\n' "$*" >&2; exit 1; }
info() { printf '%s\n' "$*" >&2; }

# fetch writes a URL to stdout, using whichever of curl/wget exists.
fetch() {
	if command -v curl >/dev/null 2>&1; then
		curl -sSfL "$1"
	elif command -v wget >/dev/null 2>&1; then
		wget -qO- "$1"
	else
		die "need curl or wget"
	fi
}

command -v tar >/dev/null 2>&1 || die "need tar"

# Map uname output onto the names goreleaser uses in archive filenames.
os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
linux | darwin) ;;
*) die "unsupported OS '$os'; see https://github.com/$REPO/releases for other builds" ;;
esac

arch=$(uname -m)
case "$arch" in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) die "unsupported architecture '$arch'; see https://github.com/$REPO/releases for other builds" ;;
esac

# Resolve the latest tag from the releases API when none was named. Parsed with
# sed rather than jq, which is not a reasonable thing to require of an installer.
version=${VERSION:-}
if [ -z "$version" ]; then
	version=$(fetch "https://api.github.com/repos/$REPO/releases/latest" |
		sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)
	[ -n "$version" ] || die "could not determine the latest release; set VERSION=vX.Y.Z"
fi

# Archives are named without the leading v, the tag keeps it.
bare=${version#v}
archive="${BINARY}_${bare}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$version"

tmp=$(mktemp -d)
# shellcheck disable=SC2064 # $tmp is expanded now on purpose
trap "rm -rf '$tmp'" EXIT INT TERM

info "Downloading $archive ($version)"
fetch "$base/$archive" > "$tmp/$archive" || die "download failed: $base/$archive"

# Verify against the release checksums when a checksum tool is available. A
# failure here is fatal: a corrupt or tampered archive is worse than no install.
if fetch "$base/checksums.txt" > "$tmp/checksums.txt" 2>/dev/null; then
	if command -v sha256sum >/dev/null 2>&1; then
		sum=$(sha256sum "$tmp/$archive" | cut -d' ' -f1)
	elif command -v shasum >/dev/null 2>&1; then
		sum=$(shasum -a 256 "$tmp/$archive" | cut -d' ' -f1)
	else
		sum=""
		info "No sha256 tool found; skipping checksum verification"
	fi
	if [ -n "$sum" ]; then
		want=$(grep " $archive\$" "$tmp/checksums.txt" | cut -d' ' -f1)
		[ -n "$want" ] || die "$archive is not listed in checksums.txt"
		[ "$sum" = "$want" ] || die "checksum mismatch for $archive (expected $want, got $sum)"
		info "Checksum OK"
	fi
else
	info "No checksums.txt for $version; skipping verification"
fi

tar -xzf "$tmp/$archive" -C "$tmp" "$BINARY" || die "could not extract $BINARY from $archive"
chmod +x "$tmp/$BINARY"

# Pick a destination: an explicit BIN_DIR, else the first writable candidate.
# Never sudo on the user's behalf — say what to run instead.
if [ -n "${BIN_DIR:-}" ]; then
	dest=$BIN_DIR
	[ -d "$dest" ] || die "BIN_DIR '$dest' does not exist"
	[ -w "$dest" ] || die "BIN_DIR '$dest' is not writable"
else
	dest=""
	for candidate in /usr/local/bin "$HOME/.local/bin"; do
		if [ -d "$candidate" ] && [ -w "$candidate" ]; then
			dest=$candidate
			break
		fi
	done
	if [ -z "$dest" ]; then
		dest=$(pwd)
		info "No writable install directory found; installing to $dest"
		info "To install system-wide instead: sudo BIN_DIR=/usr/local/bin sh install.sh"
	fi
fi

mv "$tmp/$BINARY" "$dest/$BINARY"
info "Installed $BINARY to $dest/$BINARY"

case ":${PATH}:" in
*":$dest:"*) ;;
*) info "Note: $dest is not on your PATH" ;;
esac

"$dest/$BINARY" version
