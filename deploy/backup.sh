#!/bin/sh
# Ежедневный бэкап БД: сжатый pg_dump в /backups, старше BACKUP_KEEP_DAYS дней удаляются.
# Восстановление: gunzip -c backups/expenses-<дата>.sql.gz | docker compose exec -T db psql -U app -d expenses
set -eu
while true; do
	file="/backups/expenses-$(date -u +%Y%m%d-%H%M%S).sql.gz"
	if pg_dump --no-owner --clean --if-exists | gzip > "$file.tmp"; then
		mv "$file.tmp" "$file"
		echo "backup ok: $file"
	else
		rm -f "$file.tmp"
		echo "backup FAILED" >&2
	fi
	find /backups -name 'expenses-*.sql.gz' -mtime +"${BACKUP_KEEP_DAYS:-14}" -delete
	sleep 86400
done
