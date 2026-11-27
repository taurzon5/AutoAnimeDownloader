#!/usr/bin/env bash
# A throwaway Shoko Server for the Shoko client's live test (src/internal/shoko/live_test.go,
# docs/agents/testing.md "Shoko Live Test"): one series, Frieren, fetched from AniDB through Shoko,
# and two dummy files linked by hand to its episodes 1 and 2. No AniDB account is used. Everything
# lives under runtime/shoko-test (git-ignored), and the API answers on 127.0.0.1 only.
#
#   bash scripts/shoko-live.sh up      # start (and set up the first time), write runtime/shoko-test/env.sh
#   source runtime/shoko-test/env.sh && go test ./src/internal/shoko/ -run TestShokoLive -v
#   bash scripts/shoko-live.sh down    # remove the container, keep its data
#   bash scripts/shoko-live.sh wipe    # remove the container and runtime/shoko-test
#
# `down` keeps the data on purpose: setting up asks AniDB for the series once, and AniDB bans a
# client that asks for the same anime again and again. A later `up` reuses what Shoko cached.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
PROJECT="$(cd "$HERE/.." && pwd)"
BASE="$PROJECT/runtime/shoko-test"
# v6.0.0 since 2026-11-27 (the data folder was migrated from v5.3.3; the client reads both).
IMAGE=shokoanime/server:v6.0.0
NAME=aad-shoko-test
PORT=18111
U="http://127.0.0.1:$PORT"
ANIDB_ID=17617 # Sousou no Frieren: 28 episodes, MAL 52991 (and 56885, its shorts)

case "${1:-}" in
  down) docker rm -f $NAME >/dev/null 2>&1 || true; echo "removed $NAME; its data stays in $BASE"; exit 0 ;;
  wipe) docker rm -f $NAME >/dev/null 2>&1 || true; rm -rf "$BASE"; echo "removed $NAME and $BASE"; exit 0 ;;
  up) ;;
  *) echo "usage: $0 up|down|wipe"; exit 2 ;;
esac

mkdir -p "$BASE/data" "$BASE/import/Frieren"
[ -f "$BASE/pass" ] || echo "aadtest-shoko-$(head -c6 /dev/urandom | od -An -tx1 | tr -d ' \n')" > "$BASE/pass"
PASS=$(cat "$BASE/pass")

docker rm -f $NAME >/dev/null 2>&1 || true
docker run -d --name $NAME -p "127.0.0.1:$PORT:8111" -e PUID="$(id -u)" -e PGID="$(id -g)" \
  -v "$BASE/data:/home/shoko/.shoko" -v "$BASE/import:/import" $IMAGE >/dev/null

state() { curl -s "$U/api/v3/Init/Status" | python3 -I -c 'import json,sys; print(json.load(sys.stdin)["State"])' 2>/dev/null || true; }
for i in $(seq 1 90); do s=$(state); [ -n "$s" ] && break; sleep 2; done
if [ "$s" = "Waiting" ]; then
  # First run: the default user, then start the server.
  curl -sf -o /dev/null -H 'Content-Type: application/json' \
    -d "{\"Username\":\"admin\",\"Password\":\"$PASS\"}" "$U/api/v3/Init/DefaultUser"
  curl -sf -o /dev/null "$U/api/v3/Init/StartServer"
fi
for i in $(seq 1 90); do s=$(state); [ "$s" = "Started" ] && break; sleep 2; done
[ "$s" = "Started" ] || { echo "Shoko did not start (state: $s)"; exit 1; }

KEY=$(curl -sf -H 'Content-Type: application/json' -d "{\"user\":\"admin\",\"pass\":\"$PASS\",\"device\":\"aad-live-setup\"}" \
  "$U/api/auth" | python3 -I -c 'import json,sys; print(json.load(sys.stdin)["apikey"])')
H="apikey: $KEY"
total() { curl -sf -H "$H" "$U/api/v3/$1" | python3 -I -c 'import json,sys; print(json.load(sys.stdin)["Total"])'; }

if [ "$(total 'Series?pageSize=1')" = "0" ]; then
  # The one AniDB request (Shoko's own HTTP client, rate-limited), then the series from its data.
  curl -sf -o /dev/null -X POST -H "$H" "$U/api/v3/Series/AniDB/$ANIDB_ID/Refresh?createSeriesEntry=true&immediate=true"
  for i in $(seq 1 60); do [ "$(total 'Series?pageSize=1')" != "0" ] && break; sleep 2; done
fi
SERIES=$(curl -sf -H "$H" "$U/api/v3/Series?pageSize=100" | python3 -I -c "
import json,sys
print(next(s['IDs']['ID'] for s in json.load(sys.stdin)['List'] if s['IDs']['AniDB'] == $ANIDB_ID))")

if [ "$(total 'File?pageSize=1')" = "0" ]; then
  curl -sf -o /dev/null -X POST -H "$H" -H 'Content-Type: application/json' \
    -d '{"Name":"import","Path":"/import","DropFolderType":"Source","WatchForNewFiles":false,"Size":0}' "$U/api/v3/ImportFolder"
  for ep in 01 02; do head -c 2097152 /dev/urandom > "$BASE/import/Frieren/[Test] Sousou no Frieren - $ep [1080p].mkv"; done
  curl -sf -o /dev/null -H "$H" "$U/api/v3/Action/RunImport"
  for i in $(seq 1 60); do [ "$(total 'File?pageSize=10')" = "2" ] && break; sleep 2; done
  # Link each file to its episode by hand: without an AniDB account nothing is identified.
  EPISODES=$(curl -sf -H "$H" "$U/api/v3/Series/$SERIES/Episode?includeMissing=true&includeDataFrom=AniDB&type=Episode&pageSize=0" |
    python3 -I -c 'import json,sys; print(" ".join("%d:%d" % (e["AniDB"]["EpisodeNumber"], e["IDs"]["ID"]) for e in json.load(sys.stdin)["List"]))')
  curl -sf -H "$H" "$U/api/v3/File?pageSize=10" | python3 -I -c 'import json,sys; print(" ".join("%d:%s" % (f["ID"], f["Locations"][0]["RelativePath"]) for f in json.load(sys.stdin)["List"]))' |
    tr ' ' '\n' | while IFS=: read -r fid path; do
      n=$(echo "$path" | sed -n 's/.* - \([0-9][0-9]\) .*/\1/p' | sed 's/^0//')
      eid=$(echo "$EPISODES" | tr ' ' '\n' | awk -F: -v n="$n" '$1==n{print $2}')
      curl -sf -o /dev/null -X POST -H "$H" -H 'Content-Type: application/json' -d "{\"EpisodeIDs\":[$eid]}" "$U/api/v3/File/$fid/Link"
      sleep 1 # distinct import times, for the order check
    done
fi

cat > "$BASE/env.sh" <<EOF
export AAD_LIVE_SHOKO=1
export AAD_SHOKO_URL=$U
export AAD_SHOKO_USER=admin
export AAD_SHOKO_PASSWORD=$PASS
EOF
echo "Shoko is up at $U with series $SERIES; source $BASE/env.sh"
