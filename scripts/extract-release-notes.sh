#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 3 ]; then
  echo "usage: $0 VERSION CHANGELOG OUT" >&2
  exit 2
fi

version="${1#v}"
changelog="$2"
out="$3"
tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT

awk -v version="$version" '
function section_version(line, title) {
  title = line
  sub(/^## /, "", title)
  sub(/ - .*/, "", title)
  sub(/^\[/, "", title)
  sub(/\]$/, "", title)
  sub(/^v/, "", title)
  return title
}

BEGIN {
  found = 0
  collecting = 0
  count = 0
}

/^## / {
  if (collecting) {
    collecting = 0
  } else if (section_version($0) == version) {
    found = 1
    collecting = 1
    next
  }
}

collecting {
  lines[++count] = $0
}

END {
  if (!found) {
    printf("missing changelog section for %s\n", version) > "/dev/stderr"
    exit 1
  }

  start = 1
  while (start <= count && lines[start] == "") {
    start++
  }
  while (count >= start && lines[count] == "") {
    count--
  }

  printf("# Clipfan %s\n", version)
  if (start <= count) {
    printf("\n")
    for (i = start; i <= count; i++) {
      print lines[i]
    }
  }
}
' "$changelog" >"$tmp"

mkdir -p "$(dirname "$out")"
mv "$tmp" "$out"
trap - EXIT

