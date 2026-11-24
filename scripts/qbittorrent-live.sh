#!/usr/bin/env bash
# Two throwaway qBittorrent 5.x containers for the qBittorrent backend's live test
# (src/internal/torrents/qbittorrent_live_test.go, docs/agents/testing.md "qBittorrent Live Test"):
# a seeder holding two small test torrents made here, and a downloader the backend drives. Nothing
# comes from the internet but the image; everything lives under runtime/qbt-test (git-ignored), and
# both WebUIs answer on 127.0.0.1 only.
#
#   bash scripts/qbittorrent-live.sh up      # start fresh, write runtime/qbt-test/env.sh
#   source runtime/qbt-test/env.sh && go test ./src/internal/torrents/ -run TestQBitLive -v
#   bash scripts/qbittorrent-live.sh down    # remove the containers, the network and runtime/qbt-test
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
PROJECT="$(cd "$HERE/.." && pwd)"
BASE="$PROJECT/runtime/qbt-test"
IMAGE=qbittorrentofficial/qbittorrent-nox:latest
NET=aad-qbt-test
PASS=aadtest-pass-7341

down() {
  docker rm -f aad-qbt-seeder aad-qbt-leech >/dev/null 2>&1 || true
  docker network rm $NET >/dev/null 2>&1 || true
  rm -rf "$BASE"
}
case "${1:-}" in
  down) down; echo "removed the containers, the network and $BASE"; exit 0 ;;
  up) down ;;
  *) echo "usage: $0 up|down"; exit 2 ;;
esac

mkdir -p "$BASE"/{seeder-config,seeder-data,leech-config,library,incomplete,torrents}
python3 -I "$HERE/qbittorrent-live-torrents.py" "$BASE/seeder-data" "$BASE/torrents" > "$BASE/torrents/hashes.txt"

docker network inspect $NET >/dev/null 2>&1 || docker network create $NET >/dev/null
run() { # name port extra-mounts...
  local name=$1 port=$2; shift 2
  docker rm -f "$name" >/dev/null 2>&1 || true
  # Same WebUI port inside and out: qBittorrent's Host header check refuses a port mismatch (401).
  docker run -d --name "$name" --network $NET -p "127.0.0.1:$port:$port" \
    -e QBT_LEGAL_NOTICE=confirm -e QBT_WEBUI_PORT=$port -e QBT_TORRENTING_PORT=6881 \
    -e PUID="$(id -u)" -e PGID="$(id -g)" "$@" $IMAGE >/dev/null
}
run aad-qbt-seeder 18081 -v "$BASE/seeder-config:/config" -v "$BASE/seeder-data:/data"
run aad-qbt-leech 18080 -v "$BASE/leech-config:/config" -v "$BASE/library:/library" -v "$BASE/incomplete:/incomplete"

# First start prints a temporary WebUI password; log in with it and set a known one.
setpass() { # name port
  local name=$1 port=$2 tmp="" i
  for i in $(seq 1 60); do
    tmp=$(docker logs "$name" 2>&1 | sed -n 's/.*temporary password is provided for this session: \(.*\)$/\1/p' | tail -1)
    [ -n "$tmp" ] && curl -s -o /dev/null "http://127.0.0.1:$port/" && break
    sleep 1
  done
  [ -n "$tmp" ] || { echo "no temporary password in $name's log"; exit 1; }
  local jar; jar=$(mktemp)
  # 5.x answers a good login 204 (or 200 "Ok." before Web API 2.11.8).
  code=$(curl -s -o /dev/null -w '%{http_code}' -c "$jar" -H "Referer: http://127.0.0.1:$port" --data-urlencode username=admin \
    --data-urlencode "password=$tmp" "http://127.0.0.1:$port/api/v2/auth/login")
  case "$code" in 200|204) ;; *) echo "login to $name answered $code"; exit 1 ;; esac
  curl -s -b "$jar" -H "Referer: http://127.0.0.1:$port" \
    --data-urlencode "json={\"web_ui_password\":\"$PASS\"}" "http://127.0.0.1:$port/api/v2/app/setPreferences" >/dev/null
  rm -f "$jar"
}
setpass aad-qbt-seeder 18081
setpass aad-qbt-leech 18080

# The seeder gets both torrents over its existing data and checks them.
jar=$(mktemp)
code=$(curl -s -o /dev/null -w '%{http_code}' -c "$jar" -H "Referer: http://127.0.0.1:18081" --data-urlencode username=admin \
  --data-urlencode "password=$PASS" http://127.0.0.1:18081/api/v2/auth/login)
case "$code" in 200|204) ;; *) echo "login with the new password answered $code"; exit 1 ;; esac
for t in pack single; do
  code=$(curl -s -o /dev/null -w '%{http_code}' -b "$jar" -H "Referer: http://127.0.0.1:18081" \
    -F "torrents=@$BASE/torrents/$t.torrent" -F savepath=/data http://127.0.0.1:18081/api/v2/torrents/add)
  case "$code" in 200|202) ;; *) echo "adding $t to the seeder answered $code"; exit 1 ;; esac
done
for i in $(seq 1 60); do
  states=$(curl -s -b "$jar" -H "Referer: http://127.0.0.1:18081" http://127.0.0.1:18081/api/v2/torrents/info |
    python3 -I -c 'import json,sys; print(" ".join(t["state"] for t in json.load(sys.stdin)))')
  case "$states" in *UP*UP*) break ;; esac
  sleep 1
done
rm -f "$jar"
echo "seeder states: $states"

SEEDER_IP=$(docker inspect -f "{{(index .NetworkSettings.Networks \"$NET\").IPAddress}}" aad-qbt-seeder)
PACK=$(awk '$1=="pack"{print $2}' "$BASE/torrents/hashes.txt")
SINGLE=$(awk '$1=="single"{print $2}' "$BASE/torrents/hashes.txt")
cat > "$BASE/env.sh" <<EOF
export AAD_LIVE_QBITTORRENT=1
export AAD_QBIT_URL=http://127.0.0.1:18080
export AAD_QBIT_PASSWORD=$PASS
export AAD_QBIT_LIBRARY=$BASE/library
export AAD_QBIT_LIBRARY_IN_QBIT=/library
export AAD_QBIT_INCOMPLETE=$BASE/incomplete
export AAD_QBIT_INCOMPLETE_IN_QBIT=/incomplete
export AAD_QBIT_SEED_DATA=$BASE/seeder-data
export AAD_QBIT_PACK_MAGNET='magnet:?xt=urn:btih:$PACK&dn=LivePack&x.pe=$SEEDER_IP:6881'
export AAD_QBIT_SINGLE_MAGNET='magnet:?xt=urn:btih:$SINGLE&dn=Live%20Show%20-%2003.mkv&x.pe=$SEEDER_IP:6881'
EOF
cat "$BASE/env.sh"
