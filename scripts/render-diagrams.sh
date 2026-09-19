#!/bin/sh
# Render every PlantUML source under docs/ to an SVG next to it.
# Usage: scripts/render-diagrams.sh
set -eu

root="$(cd "$(dirname "$0")/.." && pwd)"
image="cumin-plantuml:1.2025.4"

docker build --quiet --tag "$image" "$root/tools/plantuml" >/dev/null

find "$root/docs" -name '*.puml' | while read -r src; do
  dir="$(dirname "$src")"
  docker run --rm --volume "$dir:/work" "$image" -tsvg -charset UTF-8 "/work/$(basename "$src")"
  echo "rendered ${src#"$root"/}"
done
