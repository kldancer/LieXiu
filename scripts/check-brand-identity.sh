#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

patterns=(
  'github\.com/multica-ai/multica'
  '@multica/'
  'MULTICA_[A-Z0-9_]+'
  'multica://'
  'ai\.multica\.desktop'
  'ghcr\.io/multica-ai/multica'
  '(cmd|bin)/multica'
)

status=0
for pattern in "${patterns[@]}"; do
  if rg -n --hidden \
    --glob '!.git/**' \
    --glob '!node_modules/**' \
    --glob '!.work/**' \
    --glob '!docs/**' \
    --glob '!server/migrations/**' \
    --glob '!LICENSE' \
    --glob '!NOTICE' \
    --glob '!scripts/check-brand-identity.sh' \
    "$pattern" \
    apps packages server scripts .github \
    Makefile Dockerfile Dockerfile.web docker-compose.yml \
    docker-compose.selfhost.yml docker-compose.selfhost.build.yml \
    package.json pnpm-lock.yaml turbo.json .env.example .goreleaser.yml 2>/dev/null; then
    status=1
  fi
done

if (( status != 0 )); then
  echo "legacy Multica runtime identity remains" >&2
  exit 1
fi

echo "LieXiu runtime identity check passed"
