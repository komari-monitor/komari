#!/usr/bin/env bash
set -euo pipefail

# Komari 节点 IP 同步到 Git 的主脚本
# 通过读取 komari.db 的 clients 表，生成 nodes-ip.json
# 当文件变化时自动 commit + push

ENV_FILE="/etc/komari-git-sync.env"
[[ -f "$ENV_FILE" ]] && source "$ENV_FILE"

: "${KOMARI_DB:=/data/komari-monitor/data/komari.db}"
: "${WORKDIR:=/data/komari-monitor/git-sync}"
: "${BRANCH:=main}"
: "${SNAPSHOT_FILE:=nodes-ip.json}"
: "${SYNC_LOG:=/var/log/komari-git-sync.log}"

log(){ echo "[$(date '+%F %T')] $*" | tee -a "$SYNC_LOG"; }

if [[ -z "${REPO_URL:-}" ]]; then
  log "REPO_URL is empty, skip"
  exit 0
fi

if [[ ! -f "$KOMARI_DB" ]]; then
  log "DB not found: $KOMARI_DB"
  exit 1
fi

mkdir -p "$WORKDIR"
cd "$WORKDIR"

if [[ ! -d .git ]]; then
  log "clone repo: $REPO_URL"
  git clone --branch "$BRANCH" "$REPO_URL" .
fi

git fetch origin "$BRANCH" || true
git checkout "$BRANCH"
git pull --rebase origin "$BRANCH" || true

TMP=$(mktemp)
sqlite3 -json "$KOMARI_DB" "select name, ipv4, ipv6, updated_at from clients where (ipv4 is not null and ipv4!='') or (ipv6 is not null and ipv6!='') order by name asc;" > "$TMP"

cat > "$SNAPSHOT_FILE" <<JSON
{
  "generated_at": "$(date -Iseconds)",
  "source": "komari.db",
  "host": "$(hostname)",
  "nodes": $(cat "$TMP")
}
JSON
rm -f "$TMP"

if git diff --quiet -- "$SNAPSHOT_FILE"; then
  log "no changes"
  exit 0
fi

git add "$SNAPSHOT_FILE"
git commit -m "chore(komari): sync node ips $(date '+%F %T')"
git push origin "$BRANCH"
log "synced to git"
