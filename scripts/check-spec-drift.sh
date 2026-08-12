#!/usr/bin/env bash
#
# Compare the vendored OpenAPI spec against the deployed one:
#
#   vendored   internal/api/openapi-3.1.json — what the committed client in
#              internal/api is generated from, so what dbosctl actually speaks
#   deployed   $CLOUD_SPEC_URL — what DBOS Cloud serves, so what dbosctl is
#              talking to. Served publicly; no credentials needed.
#
# `servers` is excluded from the comparison: cloud repoints it at /conductor so
# that generated clients resolve paths through the proxy, and that rewrite is the
# only edit it makes. Everything else must match exactly once keys are sorted.
#
# Exits 0 when they match, 1 on drift or a fetch failure.

set -euo pipefail

VENDORED=${VENDORED:-internal/api/openapi-3.1.json}
CLOUD_SPEC_URL=${CLOUD_SPEC_URL:-https://cloud.dbos.dev/conductor/v2/openapi.json}

for tool in jq curl; do
	command -v "$tool" >/dev/null || { echo "error: $tool is required" >&2; exit 1; }
done

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# normalize strips the one field cloud rewrites and sorts keys, so a diff of two
# normalized files is a real difference in the API rather than in formatting.
normalize() { jq -S 'del(.servers)' "$1" >"$2"; }

# operations lists "METHOD /path" per operation, the readable summary of a diff.
operations() {
	jq -r '.paths | to_entries[] | .key as $p | .value | to_entries[]
	       | select(.key | IN("get","post","put","patch","delete"))
	       | "\(.key | ascii_upcase) \($p)"' "$1" | sort
}

[ -f "$VENDORED" ] || { echo "error: vendored spec not found at $VENDORED" >&2; exit 1; }
normalize "$VENDORED" "$work/vendored.json"

echo "Fetching the deployed spec from $CLOUD_SPEC_URL"
curl -fsS --max-time 60 "$CLOUD_SPEC_URL" -o "$work/deployed.raw" ||
	{ echo "error: could not fetch the deployed spec from $CLOUD_SPEC_URL" >&2; exit 1; }
normalize "$work/deployed.raw" "$work/deployed.json"

if diff -q "$work/vendored.json" "$work/deployed.json" >/dev/null; then
	echo "match: the vendored spec is what is deployed."
	exit 0
fi

echo
echo "DRIFT: the vendored spec is not what is deployed."
echo "dbosctl's generated client speaks a different API than the server it calls."
echo "Fix: re-vendor from the deployed conductor commit ('make spec'), then"
echo "'make generate', and commit the result."
echo

operations "$work/vendored.json" >"$work/vendored.ops"
operations "$work/deployed.json" >"$work/deployed.ops"
if diff -q "$work/vendored.ops" "$work/deployed.ops" >/dev/null; then
	echo "  The operation list is identical; schemas or parameters differ."
else
	echo "  operations only in the vendored spec:"
	comm -23 "$work/vendored.ops" "$work/deployed.ops" | sed 's/^/    /'
	echo "  operations only in the deployed spec:"
	comm -13 "$work/vendored.ops" "$work/deployed.ops" | sed 's/^/    /'
fi

# Written to a file and sliced with sed rather than piped through head: a
# pipeline whose reader exits early SIGPIPEs the writer, which under
# `set -o pipefail` would replace this script's exit code with 141.
diff -u "$work/vendored.json" "$work/deployed.json" >"$work/diff.txt" || true
echo "  first 60 lines of the full diff:"
sed -n '3,62p' "$work/diff.txt" | sed 's/^/    /'

exit 1
