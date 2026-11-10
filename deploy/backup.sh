#!/bin/sh
# Dumps the database of the Compose stack to a dated file and removes dumps older than KEEP_DAYS.
#
#   ./backup.sh [directory]        # default: ./backups
#   COMPOSE="podman compose" ./backup.sh     # use another Compose command
#
# Restore: see docs/deploy/backup-restore.md
set -eu
cd "$(dirname "$0")"
dir="${1:-./backups}"
keep="${KEEP_DAYS:-14}"
mkdir -p "$dir"
file="$dir/shinsou-$(date +%Y%m%d-%H%M%S).dump"
${COMPOSE:-docker compose} exec -T db pg_dump -U shinsou -d shinsou -Fc > "$file.part"
mv "$file.part" "$file"
find "$dir" -name 'shinsou-*.dump' -mtime +"$keep" -delete
echo "$file"
