# Эксплуатационный runbook

## Конфигурация и обновление

Приоритет настроек: defaults → YAML → `BORD_*`. Секреты разрешены только через
environment или `*_FILE`; для systemd используются credentials. Перед заменой
бинарника остановите сервис, создайте и проверьте backup, затем проверьте схему:

```sh
sudo systemctl stop bord
sudo -u bord bord-server backup --offline --database /var/lib/bord/bord.db --blobs /var/lib/bord/blobs --output /var/lib/bord/backups/pre-upgrade
sudo -u bord bord-server restore-verify --backup /var/lib/bord/backups/pre-upgrade
sudo -u bord bord-server migrate --database /var/lib/bord/bord.db
sudo systemctl start bord
```

Exit code `3` у `status`/`migrate` означает, что схема новее бинарника: не
запускайте старый бинарник и восстановите совместимую версию приложения.

## Backup и восстановление

Backup требует остановленного сервиса (`--offline`), создает SQLite snapshot
через `VACUUM INTO`, копирует только обычные blob-файлы и записывает SHA-256,
размер и относительный путь каждого файла в `manifest.json`. Храните backup вне
`/var/lib/bord/blobs` и копируйте его во внешнее зашифрованное хранилище.

```sh
sudo systemctl stop bord
sudo -u bord bord-server restore-verify --backup /srv/restore/candidate
sudo -u bord bord-server restore --offline --backup /srv/restore/candidate --destination /var/lib/bord/restore-candidate
sudo -u bord bord-server status --database /var/lib/bord/restore-candidate/database.sqlite3
```

После проверки замените рабочую директорию атомарным rename на том же
filesystem, исправьте пути `database.sqlite3` и `blobs`, запустите сервис и
проверьте `/readyz`, чтение тестового поста и скачивание известного вложения.
Restore отклоняет измененные, лишние, отсутствующие, символьные и небезопасно
именованные файлы.

## Ротация ключей

Никогда не заменяйте lookup-ключ: существующие HMAC и блокировки перестанут
совпадать. Проверяемая процедура ключа шифрования описана в
[key-rotation.md](key-rotation.md). Команда делает проверенный backup, меняет
данные ограниченными транзакциями и безопасно продолжается после прерывания.

## Retention и privacy

Значение по умолчанию — 90 дней. Исключения допустимы только для активной
блокировки или документированного расследования. Следите за агрегированным
событием `retention.completed`; оно содержит тип, результат и count, но не IP и
не идентификаторы записей. До публичного запуска оператор обязан опубликовать
privacy policy со сроком хранения, назначением anti-abuse данных, порядком
раскрытия IP и каналом обращений.

## Администраторы и блокировки

Локальные lifecycle-команды требуют остановленного сервиса. Credential создается
с режимом `0600` и никогда не выводится в stdout/stderr:

```sh
sudo systemctl stop bord
sudo -u bord bord-server admin create --offline --database /var/lib/bord/bord.db --id root --role administrator --ttl 720h --credential-output /var/lib/bord/admin-root.credential
sudo -u bord bord-server admin rotate --offline --database /var/lib/bord/bord.db --id root --role administrator --ttl 720h --credential-output /var/lib/bord/admin-root.next --confirm ROTATE
sudo -u bord bord-server admin revoke --offline --database /var/lib/bord/bord.db --id old-operator --reason "смена дежурной группы" --confirm REVOKE
sudo systemctl start bord
```

Файл назначения должен отсутствовать. После rotate атомарно обновите ссылку
secret manager и удалите прежнюю копию. Роли: `reports`, `moderator`,
`administrator`. Создание/revoke блокировок и hide/restore/close выполняются
через management API с bearer из защищенного файла; raw IP допустим только в
поле `subject` ban-запроса, но не в причине или журнале. Блокировка всегда имеет
операции, причину и срок; после создания проверьте нейтральный публичный отказ и
audit request ID. Не редактируйте SQLite вручную.

CLI вызывает management API, читает bearer только из файла `0600` и не печатает
его. Пример блокировки lookup для создания ответов и последующего отзыва:

```sh
bord-server admin ban --management-url http://127.0.0.1:8081 --credential-file /var/lib/bord/admin-root.credential --subject-type lookup --subject 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef --operations create_post --ttl 2h --reason "автоматизированный flood"
bord-server admin unban --management-url http://127.0.0.1:8081 --credential-file /var/lib/bord/admin-root.credential --ban-id 00000001 --reason "апелляция принята" --confirm REVOKE-BAN
```

Management listener оставляйте на loopback/private management network. Для
raw-IP используйте `--subject-type ip`; адрес не попадает в audit reason.

Retention выполняется при старте и далее каждые 24 часа. До удаления получите
отчет. Ручное удаление требует остановки, нового проверяемого backup и точного
подтверждения; legal hold и активные блокировки не удаляются:

```sh
sudo -u bord bord-server retention report --database /var/lib/bord/bord.db
sudo systemctl stop bord
sudo -u bord bord-server retention hold --offline --database /var/lib/bord/bord.db --subject-id 42 --confirm HOLD
sudo -u bord bord-server retention release --offline --database /var/lib/bord/bord.db --subject-id 42 --confirm RELEASE
sudo -u bord bord-server retention run --offline --database /var/lib/bord/bord.db --blobs /var/lib/bord/blobs --backup /var/lib/bord/backups/pre-retention-20261017 --confirm DELETE
sudo systemctl start bord
```

Освобождение hold выполняйте только по закрытому расследованию. JSON-отчет
содержит лишь агрегаты `EligibleIP`, `LegalHoldIP`, `ActiveBanIP`,
`Idempotency`, `Challenges`.

## Инцидент и recovery

1. Снимите сервис с readiness и остановите запись.
2. Сохраните безопасные агрегированные логи и request IDs; не экспортируйте raw IP.
3. Проверьте последний backup и восстановите его в новый каталог.
4. Запустите `status`, затем изолированную readiness/smoke-проверку.
5. Выполните cutover атомарным rename и сохраните старую директорию read-only.
6. После инцидента ротируйте затронутые credentials и документируйте потери.

## SSE smoke за Caddy

В staging откройте поток `curl -N --http2 https://HOST/api/v1/events?...`,
дождитесь heartbeat, создайте тестовый ответ другим клиентом и убедитесь, что
событие приходит без задержки. Затем оборвите соединение и переподключитесь с
`Last-Event-ID`; проверьте resume или `stream.reset`. Эта проверка требует
реализованного HTTP/SSE lane и реального staging, поэтому не заменяется одной
валидацией Caddyfile. Локальный эквивалент, проверяющий heartbeat и reconnect:

```sh
CADDY_BIN=/path/to/caddy ./scripts/staging-smoke.sh
```
