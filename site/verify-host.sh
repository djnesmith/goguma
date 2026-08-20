#!/bin/bash
#
# Compares a candidate host against production, byte for byte, before any DNS
# changes.
#
#   site/verify-host.sh https://goguma.netlify.app
#
# The point of a migration is that nobody notices it happened. That is only
# true if the new host serves the same bytes at the same paths, and the only
# way to know is to fetch both and compare — not to read a dashboard that says
# "Published".
#
# It also checks the two things a static-host migration actually gets wrong:
# whether the cache headers took effect, and whether `advisories.json` still
# carries a signature that verifies. That file is fetched by every installed
# copy of goguma; serving it wrong is worse than not serving it at all.
set -uo pipefail

CAND="${1:-}"
PROD="${2:-https://getgoguma.com}"
[[ -n "$CAND" ]] || { echo "usage: $0 <candidate-url> [production-url]" >&2; exit 2; }
CAND="${CAND%/}"; PROD="${PROD%/}"

SITE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PUBKEY="Vlgap6w8ICfbQn8G8wE+kh0D6kETT9jQB1Z3aVGAFFM="

# Every path the site serves, derived from what deploy.sh would publish rather
# than from a list kept by hand here, which would drift the first time a page
# was added.
cd "$SITE"

# The path list comes from deploy.sh's own dry run, which is the authoritative
# answer to "what does this site consist of".
#
# It was a glob over the working tree, which is not the same thing and got it
# wrong twice: blog/posts/ is the markdown the articles are generated FROM and
# is deliberately not published, and assets/cursor.png is referenced by nothing
# and does not ship either. Both were reported as the new host missing a file.
PATHS=()
while IFS= read -r f; do
    case "$f" in
        # Host configuration, not content. Netlify consumes _headers and does
        # not serve it; comparing them would fail on every correct migration.
        _headers|vercel.json) continue ;;
        index.html)        PATHS+=("/") ;;
        */index.html)      PATHS+=("/${f%index.html}") ;;
        *)                 PATHS+=("/$f") ;;
    esac
done < <("$SITE/deploy.sh" 2>/dev/null | awk '/^ +[0-9]+K  /{print $2}')

[[ ${#PATHS[@]} -gt 0 ]] || { echo "deploy.sh listed nothing; cannot verify" >&2; exit 2; }

echo "comparing ${#PATHS[@]} paths"
echo "  candidate:  $CAND"
echo "  production: $PROD"
echo

fail=0
for p in "${PATHS[@]}"; do
    # -L, because Cloudflare Pages strips .html from URLs: /404.html answers
    # 308 to /404 and serves correctly from there. Without following, a
    # correct host looked like it was missing the page.
    ca=$(curl -sSL -o /tmp/_c -w "%{http_code}" "$CAND$p" 2>/dev/null)
    pa=$(curl -sSL -o /tmp/_p -w "%{http_code}" "$PROD$p" 2>/dev/null)
    if [[ "$ca" != "200" ]]; then
        printf "  MISSING  %-46s candidate=%s\n" "$p" "$ca"; fail=1; continue
    fi
    if [[ "$pa" == "200" ]]; then
        cs=$(shasum -a 256 /tmp/_c | cut -d' ' -f1)
        ps=$(shasum -a 256 /tmp/_p | cut -d' ' -f1)
        if [[ "$cs" != "$ps" ]]; then
            printf "  DIFFERS  %-46s\n" "$p"; fail=1; continue
        fi
    fi
    printf "  ok       %-46s\n" "$p"
done

echo
echo "cache headers on the candidate:"
for p in /assets/potato.webp /advisories.json /; do
    cc=$(curl -sSI "$CAND$p" | tr -d '\r' | awk -F': ' 'tolower($1)=="cache-control"{print $2}')
    printf "  %-24s %s\n" "$p" "${cc:-(none — _headers did not take effect)}"
    [[ -z "$cc" ]] && fail=1
done

echo
echo "advisory feed:"
curl -sS "$CAND/advisories.json" -o /tmp/_adv
latest=$(python3 -c "import json;print(json.load(open('/tmp/_adv'))['latest'])" 2>/dev/null)
if [[ -z "$latest" ]]; then
    echo "  UNREADABLE — every installed goguma reads this file"; fail=1
else
    echo "  latest: $latest"
    if [[ -x "$SITE/../bin/goguma-advisory" ]]; then
        V="$SITE/../bin/goguma-advisory verify"
    else
        V="go run $SITE/../cmd/goguma-advisory verify"
    fi
    if $V --pub "$PUBKEY" < /tmp/_adv >/dev/null 2>&1; then
        echo "  signature: ok"
    else
        echo "  signature: FAILED TO VERIFY"; fail=1
    fi
fi

echo
if [[ $fail -eq 0 ]]; then
    echo "The candidate serves the same site. Safe to point DNS at it."
else
    echo "Differences above. Do not change DNS." >&2
fi
exit $fail
