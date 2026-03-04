#!/bin/sh
# gc dolt list — List Dolt databases with their filesystem paths.
#
# Shows databases for the HQ (city) and all configured rigs.
#
# Environment: GC_CITY_PATH
set -e

data_dir="$GC_CITY_PATH/.gc/dolt-data"

if [ ! -d "$data_dir" ]; then
  echo "No databases found."
  exit 0
fi

found=0
for d in "$data_dir"/*/; do
  [ ! -d "$d/.dolt" ] && continue
  name="$(basename "$d")"
  # Skip system databases.
  case "$name" in
    information_schema|mysql|dolt_cluster) continue ;;
  esac
  printf "%s\t%s\n" "$name" "$d"
  found=$((found + 1))
done

if [ "$found" -eq 0 ]; then
  echo "No databases found."
fi
