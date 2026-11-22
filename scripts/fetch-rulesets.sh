#!/bin/sh
# Downloads the rule sets that ship inside the app into resources/rulesets/.
set -eu
DIR="$(cd "$(dirname "$0")/.." && pwd)/resources/rulesets"
mkdir -p "$DIR"
get() { # repo name
  for base in "https://cdn.jsdelivr.net/gh/SagerNet/$1@rule-set" "https://raw.githubusercontent.com/SagerNet/$1/rule-set"; do
    if curl -fsSL --max-time 60 -o "$DIR/$2.srs.tmp" "$base/$2.srs" && [ "$(head -c3 "$DIR/$2.srs.tmp")" = "SRS" ]; then
      mv "$DIR/$2.srs.tmp" "$DIR/$2.srs"; echo "ok  $2 ($(wc -c < "$DIR/$2.srs") bytes)"; return 0
    fi
  done
  rm -f "$DIR/$2.srs.tmp"; echo "FAILED $2" >&2; return 1
}
# The list is data, shared with the program: internal/ruleset/sets.list.
LIST="$(cd "$(dirname "$0")/.." && pwd)/internal/ruleset/sets.list"
sed 's/#.*//' "$LIST" | while read -r repo name; do
  [ -n "${name:-}" ] && get "$repo" "$name"
done
