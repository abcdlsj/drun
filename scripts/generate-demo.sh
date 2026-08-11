#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "$0")/.." && pwd)
output_dir="$repo_root/assets"
work_dir=$(mktemp -d "${TMPDIR:-/tmp}/drun-demo.XXXXXX")
trap 'rm -rf "$work_dir"' EXIT

font="Menlo-Regular"

mkdir -p "$output_dir"

render_frame() {
  local frame_number=$1
  local active_step=$2
  local completed_steps=$3
  local prompt=$4
  local final_message=$5
  local frame_file
  frame_file=$(printf '%s/frame-%02d.svg' "$work_dir" "$frame_number")

  {
    cat <<SVG
<svg xmlns="http://www.w3.org/2000/svg" width="1100" height="650" viewBox="0 0 1100 650">
  <rect width="1100" height="650" rx="16" fill="#10141b"/>
  <rect width="1100" height="44" rx="16" fill="#202733"/>
  <rect y="28" width="1100" height="16" fill="#202733"/>
  <circle cx="24" cy="22" r="7" fill="#ff5f57"/>
  <circle cx="48" cy="22" r="7" fill="#febc2e"/>
  <circle cx="72" cy="22" r="7" fill="#28c840"/>
  <text x="550" y="28" text-anchor="middle" fill="#8b95a7" font-family="$font" font-size="14">drun demo</text>

  <text x="44" y="100" fill="#39d0d8" font-family="$font" font-size="20" font-weight="700">drun</text>
  <text x="102" y="100" fill="#7f8a9a" font-family="$font" font-size="16">safe container refresh</text>

  <text x="44" y="151" fill="#5ee38e" font-family="Arial" font-size="17">✓</text>
  <text x="70" y="151" fill="#e7edf5" font-family="$font" font-size="17">Inspecting container configuration</text>

SVG

    if (( completed_steps == 0 )); then
      cat <<SVG
  <text x="44" y="190" fill="#39d0d8" font-family="$font" font-size="17">$active_step</text>
  <text x="70" y="190" fill="#e7edf5" font-family="$font" font-size="17">Checking the latest image</text>
SVG
    else
      cat <<SVG
  <text x="44" y="190" fill="#5ee38e" font-family="Arial" font-size="17">✓</text>
  <text x="70" y="190" fill="#e7edf5" font-family="$font" font-size="17">Checking the latest image</text>
SVG
    fi

    local y=229
    if (( completed_steps >= 1 )); then
      cat <<SVG
  <text x="44" y="229" fill="#39d0d8" font-family="$font" font-size="15" font-weight="700">update</text>
  <text x="112" y="229" fill="#e7edf5" font-family="$font" font-size="16" font-weight="700">web</text>
  <text x="164" y="229" fill="#e7edf5" font-family="$font" font-size="15">v1.4.2 → v1.5.0</text>
  <text x="112" y="255" fill="#7f8a9a" font-family="$font" font-size="15">ghcr.io/acme/web:main</text>

  <text x="44" y="293" fill="#39d0d8" font-family="$font" font-size="15" font-weight="700">command</text>
  <line x1="48" y1="310" x2="48" y2="366" stroke="#39d0d8" stroke-width="2"/>
  <text x="68" y="330" fill="#e7edf5" font-family="$font" font-size="15">docker run -d --name web --restart unless-stopped --publish 8080:80/tcp</text>
  <text x="68" y="356" fill="#e7edf5" font-family="$font" font-size="15">--volume web-data:/srv/data --network app ghcr.io/acme/web:main</text>
SVG
      y=405
    fi
    local steps=(
      "Stopping the current container"
      "Creating a rollback point"
      "Starting the replacement container"
      "Removing the rollback point"
    )
    local replacement_steps=$((completed_steps - 1))
    local index
    for index in "${!steps[@]}"; do
      if (( index < replacement_steps )); then
        printf '  <text x="44" y="%d" fill="#5ee38e" font-family="Arial" font-size="16">✓</text>\n' "$y"
        printf '  <text x="70" y="%d" fill="#e7edf5" font-family="%s" font-size="16">%s</text>\n' "$y" "$font" "${steps[$index]}"
        y=$((y + 31))
      elif (( index == replacement_steps )) && [[ -n "$active_step" ]]; then
        printf '  <text x="44" y="%d" fill="#66d9ef" font-family="%s" font-size="17">%s</text>\n' "$y" "$font" "$active_step"
        printf '  <text x="70" y="%d" fill="#e7edf5" font-family="%s" font-size="16">%s</text>\n' "$y" "$font" "${steps[$index]}"
        y=$((y + 31))
      fi
    done

    if [[ -n "$prompt" ]]; then
      printf '  <text x="44" y="%d" fill="#e7edf5" font-family="%s" font-size="16">Replace the container with this configuration?</text>\n' "$y" "$font"
      y=$((y + 34))
      printf '  <text x="44" y="%d" fill="#39d0d8" font-family="%s" font-size="16" font-weight="700">● Keep current</text>\n' "$y" "$font"
      printf '  <text x="190" y="%d" fill="#7f8a9a" font-family="%s" font-size="16">○ Replace</text>\n' "$y" "$font"
      y=$((y + 27))
      printf '  <text x="44" y="%d" fill="#7f8a9a" font-family="%s" font-size="14">←→ select · enter confirm</text>\n' "$y" "$font"
    fi
    if [[ -n "$final_message" ]]; then
      printf '  <text x="44" y="%d" fill="#5ee38e" font-family="Arial" font-size="17">✓</text>\n' "$y"
      printf '  <text x="70" y="%d" fill="#e7edf5" font-family="%s" font-size="16" font-weight="700">Container web updated v1.4.2 → v1.5.0</text>\n' "$y" "$font"
    fi

    cat <<'SVG'
</svg>
SVG
  } > "$frame_file"
}

spinner_frames=("|" "/" "-" "\\")
frame=1

for spinner in "${spinner_frames[@]}"; do
  render_frame "$frame" "$spinner" 0 "" ""
  frame=$((frame + 1))
done
for _ in {1..10}; do
  render_frame "$frame" "" 1 "yes" ""
  frame=$((frame + 1))
done

for step in 1 2 3 4; do
  for spinner in "${spinner_frames[@]:0:4}"; do
    render_frame "$frame" "$spinner" "$step" "" ""
    frame=$((frame + 1))
  done
done
render_frame "$frame" "" 5 "" "done"

magick -background none "$work_dir"/frame-*.svg \
  -set delay 9 -set dispose previous \
  \( +clone -set delay 150 \) -swap -1,-2 +delete \
  -loop 0 -layers Optimize "$output_dir/demo.gif"

printf 'Generated %s\n' "$output_dir/demo.gif"
