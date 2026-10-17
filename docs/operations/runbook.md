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

Никогда не заменяйте lookup-ключ без плана переиндексации: существующие HMAC не
будут находиться. Ключ шифрования ротируется добавлением нового `key_id`,
сохранением предыдущего ключа для чтения до завершения re-encryption и только
затем удалением старого ключа. Каждую ротацию предваряет проверенный backup;
значения ключей не записываются в ticket, audit или логи.

## Retention и privacy

Значение по умолчанию — 90 дней. Исключения допустимы только для активной
блокировки или документированного расследования. Следите за агрегированным
событием `retention.completed`; оно содержит тип, результат и count, но не IP и
не идентификаторы записей. До публичного запуска оператор обязан опубликовать
privacy policy со сроком хранения, назначением anti-abuse данных, порядком
раскрытия IP и каналом обращений.

## Администраторы и блокировки

Первого администратора создавайте локальной bootstrap-командой после запуска;
последующие online-команды должны идти через management listener и оставлять
audit. Credential передавайте через защищенный файл/stdin поддерживаемого CLI,
не как аргумент. Блокировка всегда имеет scope, операции, причину и срок; после
создания проверьте нейтральный публичный отказ и audit request ID. Точные admin
команды пока не подключены к `bord-server`: это блокирует публичный запуск и
закрытие task 7.5. Не обходите ограничение прямым редактированием SQLite.

Retention выполняется при старте и далее каждые 24 часа. Отдельной команды для
ручного запуска и безопасной команды re-encryption при ротации ключа пока нет;
до их появления ротацию нельзя считать проверенной операторской процедурой.

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
