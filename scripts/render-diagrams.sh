#!/bin/sh
# Render PlantUML sources to SVG.
# Usage:
#   scripts/render-diagrams.sh                    every .puml under docs/, to an SVG next to it
#   scripts/render-diagrams.sh <in.puml> <out.svg>  one source, to the given SVG path
set -eu

root="$(cd "$(dirname "$0")/.." && pwd)"
image="cumin-plantuml:1.2025.4"

if [ $# -ne 0 ] && [ $# -ne 2 ]; then
  sed -n '2,5p' "$0" | sed 's/^# \{0,1\}//' >&2
  exit 2
fi

docker build --quiet --tag "$image" "$root/tools/plantuml" >/dev/null

if [ $# -eq 2 ]; then
  # One diagram outside docs/, for example the diagram of an issue. The source
  # goes in and the SVG comes out through the pipe, so any path works.
  [ -f "$1" ] || { echo "error: no such file: $1" >&2; exit 1; }
  docker run --rm -i "$image" -tsvg -charset UTF-8 -pipe <"$1" >"$2"
  echo "rendered $2"
  exit 0
fi

find "$root/docs" -name '*.puml' | while read -r src; do
  dir="$(dirname "$src")"
  docker run --rm --volume "$dir:/work" "$image" -tsvg -charset UTF-8 "/work/$(basename "$src")"
  echo "rendered ${src#"$root"/}"
done
