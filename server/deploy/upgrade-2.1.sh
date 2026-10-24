#!/bin/sh
set -eu
NEW=${1:-/tmp/lumina-upgrade-2.1}
BASE=/mnt/sda1/.lumina
BACKUP="$BASE/upgrade-backup-$(date +%Y%m%d-%H%M%S)"
test -s "$NEW"
mkdir "$BACKUP"
cp "$BASE/bin/lumina" "$BACKUP/lumina"
cp /etc/init.d/lumina "$BACKUP/lumina.init"
/etc/init.d/lumina stop
sleep 2
for file in lumina.db lumina.db-wal lumina.db-shm; do
 if [ -f "$BASE/$file" ]; then cp "$BASE/$file" "$BACKUP/$file"; fi
done
cp "$NEW" "$BASE/bin/lumina.new"
chmod 755 "$BASE/bin/lumina.new"
mv "$BASE/bin/lumina.new" "$BASE/bin/lumina"
/etc/init.d/lumina start
n=0
while [ "$n" -lt 20 ]; do
 sleep 1
 if wget -qO- http://127.0.0.1:8088/api/health | grep -q '2.1.0'; then
  echo "UPGRADE_OK backup=$BACKUP"
  wget -qO- http://127.0.0.1:8088/api/health
  exit 0
 fi
 n=$((n+1))
done
/etc/init.d/lumina stop
cp "$BACKUP/lumina" "$BASE/bin/lumina"
chmod 755 "$BASE/bin/lumina"
/etc/init.d/lumina start
echo "UPGRADE_FAILED previous binary restored; database backup=$BACKUP" >&2
exit 1
