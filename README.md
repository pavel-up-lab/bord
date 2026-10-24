# Bord

[Публичная борда](https://bord.pw) · [Скачать TUI](https://bord.pw/#install) ·
[Руководство TUI](docs/tui.md) · [Admin TUI](docs/admin-tui.md) · [Безопасность](SECURITY.md) · [MIT License](LICENSE)

Bord — публичная анонимная терминальная борда. Репозиторий содержит три
исполняемых файла:

- `bord-server` — HTTP API, SSE-уведомления, SQLite-хранилище, обработка
  изображений и management API;
- `bord` — полноэкранный TUI-клиент в стиле Midnight Commander с клавиатурой,
  мышью, темами и поддержкой JPEG/PNG.
- `bord-admin` — приватный TUI для жалоб, блокировок, аудита и lifecycle досок;
  он также встраивает обычный пользовательский режим, но management API и
  credential остаются изолированы от public transport.

Аккаунты не требуются. Клиент локально хранит анонимный session secret и ключи,
позволяющие позднее удалить собственные публикации. Сервер не хранит исходный
IP без явного включения этой функции.

## Содержание

- [Системные требования](#системные-требования)
- [Публичный сервер](#публичный-сервер)
- [Сборка](#сборка)
- [Быстрый локальный запуск](#быстрый-локальный-запуск)
- [Конфигурация backend](#конфигурация-backend)
- [Production: systemd и Caddy](#production-systemd-и-caddy)
- [Запуск TUI](#запуск-tui)
- [Ретро landing](#ретро-landing)
- [Управление TUI](#управление-tui)
- [Обновление и резервное копирование](#обновление-и-резервное-копирование)
- [Диагностика](#диагностика)
- [Разработка](#разработка)
- [Участие в проекте](#участие-в-проекте)

## Публичный сервер

Landing, загрузки клиентов и API доступны на **https://bord.pw**. После
установки клиента подключитесь командой:

```sh
bord --server https://bord.pw
```

Публичный экземпляр предназначен для использования сервиса. Для собственного
развёртывания следуйте production-инструкциям ниже и опубликуйте отдельную
privacy policy оператора.

## Системные требования

### Backend

- Go 1.27 или готовый бинарник `bord-server`;
- Linux для предоставленного systemd unit;
- Caddy 2.8+ для публичного HTTPS и корректного проксирования SSE;
- DNS-запись на сервер и открытые TCP-порты 80/443.

SQLite встроен через pure-Go драйвер: системная библиотека SQLite и C toolchain
для сборки не нужны.

### TUI

Поддерживаются Linux, macOS и современный Windows Terminal. Старый Windows
`cmd.exe` не поддерживается. Для корректного отображения нужны UTF-8, ANSI-цвета
и шрифт с символами рамок.

## Сборка

Из корня репозитория:

```sh
mkdir -p dist
CGO_ENABLED=0 go build -trimpath -o dist/bord-server ./cmd/bord-server
CGO_ENABLED=0 go build -trimpath -o dist/bord ./cmd/bord
CGO_ENABLED=0 go build -trimpath -o dist/bord-admin ./cmd/bord-admin
```

Проверить версии:

```sh
./dist/bord-server version
./dist/bord version
./dist/bord-admin version
```

Примеры кросс-компиляции клиента:

```sh
GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o dist/bord-linux-amd64 ./cmd/bord
GOOS=darwin  GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -o dist/bord-darwin-arm64 ./cmd/bord
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o dist/bord-windows-amd64.exe ./cmd/bord
```

## Быстрый локальный запуск

Ниже используется отдельный каталог `/tmp/bord-local`; данные проекта и
пользовательская конфигурация не затрагиваются.

### 1. Подготовить данные и lookup-ключ

Lookup-ключ обязателен даже при выключенном хранении исходных IP. Файл содержит
base64-представление 32 случайных байт:

```sh
mkdir -p /tmp/bord-local/{data/blobs,secrets,client}
openssl rand -base64 32 > /tmp/bord-local/secrets/ip_lookup_key
chmod 600 /tmp/bord-local/secrets/ip_lookup_key
```

Не добавляйте этот файл в Git и не используйте один ключ в разных окружениях.

### 2. Запустить backend

```sh
env \
  BORD_HTTP_ADDRESS=127.0.0.1:8080 \
  BORD_DATABASE=/tmp/bord-local/data/bord.db \
  BORD_BLOBS=/tmp/bord-local/data/blobs \
  BORD_IP_LOOKUP_KEY_FILE=/tmp/bord-local/secrets/ip_lookup_key \
  ./dist/bord-server serve
```

`serve` автоматически применяет недостающие миграции и создаёт доску
`/general/`. Проверка состояния:

```sh
curl --fail http://127.0.0.1:8080/healthz
curl --fail http://127.0.0.1:8080/readyz
curl --fail http://127.0.0.1:8080/api/v1/boards
```

### 3. Запустить TUI

Во втором терминале:

```sh
./dist/bord \
  --server http://127.0.0.1:8080 \
  --config /tmp/bord-local/client/state.json \
  --theme classic \
  --inline off
```

Для тестовой базы с четырьмя досками, тредами и ответами сначала остановите
backend и выполните:

```sh
./dist/bord-server demo-seed \
  --database /tmp/bord-local/data/bord.db \
  --blobs /tmp/bord-local/data/blobs \
  --confirm DEMO-SEED
```

Повторный запуск команды идемпотентен. `demo-seed` предназначен только для
локального тестирования; подробности и безопасный reset описаны в
[`docs/operations/demo-data.md`](docs/operations/demo-data.md).

## Конфигурация backend

Приоритет значений: встроенные defaults → YAML-файл → переменные `BORD_*`.

Запуск с YAML:

```sh
bord-server serve --config /etc/bord/config.yaml
```

Пример:

```yaml
http:
  address: 127.0.0.1:8080
  max_body_bytes: 16777216
  body_read_timeout: 15s
storage:
  database: /var/lib/bord/bord.db
  blobs: /var/lib/bord/blobs
privacy:
  store_raw_ip: false
```

Основные переменные окружения:

| Переменная | Назначение | Значение по умолчанию |
|---|---|---|
| `BORD_HTTP_ADDRESS` | адрес публичного HTTP listener | `127.0.0.1:8080` |
| `BORD_MANAGEMENT_ADDRESS` | отдельный management listener; оставьте пустым, если не нужен | выключен |
| `BORD_HTTP_MAX_BODY_BYTES` | максимальный размер HTTP body | `1048576` |
| `BORD_HTTP_BODY_READ_TIMEOUT` | timeout чтения body | `15s` |
| `BORD_DATABASE` | путь к SQLite | `bord.db` |
| `BORD_BLOBS` | каталог обработанных вложений | `data/blobs` |
| `BORD_STORE_RAW_IP` | хранить зашифрованный исходный IP | `false` |
| `BORD_TRUSTED_PROXIES` | CIDR reverse proxy, чьим IP-заголовкам доверяет API | пусто |
| `BORD_IP_LOOKUP_KEY` / `_FILE` | обязательный HMAC lookup-ключ | — |
| `BORD_IP_ENCRYPTION_KEY` / `_FILE` | AES-256 ключ для raw IP | — |
| `BORD_IP_ENCRYPTION_KEY_ID` | идентификатор текущего encryption key | `primary` |

Секрет можно передать либо значением, либо через вариант `_FILE`, но не обоими
способами одновременно. Содержимое должно быть standard base64 без кавычек:

- lookup key — не менее 32 байт после декодирования;
- encryption key — ровно 32 байта и обязателен только при
  `BORD_STORE_RAW_IP=true`.

Lookup-ключ нельзя просто заменить: изменятся HMAC-идентификаторы anti-abuse.
Ротация encryption key выполняется по процедуре
[`docs/operations/key-rotation.md`](docs/operations/key-rotation.md).

SQLite работает в WAL-режиме. Не редактируйте базу вручную и не копируйте только
файл `.db` у работающего процесса — используйте штатную backup-команду.

## Production: systemd и Caddy

Готовые шаблоны находятся в `deploy/systemd/` и `deploy/Caddyfile`. Management
listener не должен быть доступен из публичной сети.

### 1. Установить пользователя, бинарник и каталоги

```sh
sudo useradd --system --home-dir /var/lib/bord --shell /usr/sbin/nologin bord
sudo install -d -m 0700 -o bord -g bord /var/lib/bord /var/lib/bord/blobs
sudo install -d -m 0750 -o root -g bord /etc/bord
sudo install -d -m 0700 -o root -g root /etc/bord/secrets
sudo install -m 0755 dist/bord-server /usr/bin/bord-server
sudo install -m 0644 deploy/systemd/bord.service /etc/systemd/system/bord.service
sudo install -m 0640 -o root -g bord deploy/systemd/config.yaml.example /etc/bord/config.yaml
sudo install -m 0640 -o root -g bord deploy/systemd/bord.env.example /etc/bord/bord.env
```

### 2. Создать секреты

Если raw-IP retention не нужен, установите в `/etc/bord/config.yaml`
`store_raw_ip: false`; encryption key при этом не используется. Предоставленный
unit загружает оба файла как systemd credentials, поэтому безопаснее создать оба:

```sh
sudo sh -c 'umask 077; openssl rand -base64 32 > /etc/bord/secrets/ip_lookup_key'
sudo sh -c 'umask 077; openssl rand -base64 32 > /etc/bord/secrets/ip_encryption_key'
sudo chown root:root /etc/bord/secrets/ip_lookup_key /etc/bord/secrets/ip_encryption_key
sudo chmod 600 /etc/bord/secrets/ip_lookup_key /etc/bord/secrets/ip_encryption_key
```

Не печатайте значения в терминал, CI-лог или shell history. Сохраните защищённую
резервную копию ключей отдельно от backup данных.

### 3. Настроить backend environment

Проверьте `/etc/bord/bord.env`:

```dotenv
BORD_HTTP_ADDRESS=127.0.0.1:8080
BORD_MANAGEMENT_ADDRESS=127.0.0.1:8081
BORD_DATABASE=/var/lib/bord/bord.db
BORD_BLOBS=/var/lib/bord/blobs
BORD_STORE_RAW_IP=false
BORD_TRUSTED_PROXIES=127.0.0.1/32,::1/128
```

`BORD_TRUSTED_PROXIES` должен содержать только адреса Caddy, непосредственно
подключающиеся к backend. Иначе сервер либо не увидит IP клиента, либо начнёт
доверять поддельным proxy-заголовкам.

### 4. Инициализировать базу и запустить systemd service

```sh
sudo -u bord /usr/bin/bord-server migrate --database /var/lib/bord/bord.db
sudo -u bord /usr/bin/bord-server status --database /var/lib/bord/bord.db
sudo systemd-analyze verify /etc/systemd/system/bord.service
sudo systemctl daemon-reload
sudo systemctl enable --now bord.service
sudo systemctl status bord.service
curl --fail http://127.0.0.1:8080/readyz
```

При необходимости заранее создайте локального администратора. Credential
записывается в файл и не выводится в stdout:

```sh
sudo systemctl stop bord.service
sudo -u bord bord-server admin create \
  --offline \
  --database /var/lib/bord/bord.db \
  --id root \
  --role administrator \
  --ttl 720h \
  --credential-output /var/lib/bord/admin-root.credential
sudo systemctl start bord.service
```

### 5. Настроить Caddy и HTTPS

```sh
sudo install -m 0644 deploy/Caddyfile /etc/caddy/Caddyfile
sudo systemctl edit caddy
```

Добавьте override, заменив домен:

```ini
[Service]
Environment=BORD_DOMAIN=board.example.org
Environment=BORD_UPSTREAM=127.0.0.1:8080
Environment=BORD_UPSTREAM_PROXY_CIDRS=127.0.0.1/32
```

Затем:

```sh
sudo systemctl daemon-reload
caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile
sudo systemctl reload caddy
curl --fail https://board.example.org/healthz
curl --fail https://board.example.org/readyz
curl --fail https://board.example.org/api/v1/boards
```

Caddyfile отдельно обрабатывает `/api/v1/events`: отключает буферизацию и
response timeout, чтобы SSE-события доставлялись сразу. Публикуйте наружу только
80/443; порты 8080 и 8081 должны оставаться loopback/private.

Полный production checklist, staging smoke и требования к privacy policy:

- [`docs/operations/install.md`](docs/operations/install.md);
- [`docs/operations/release-checklist.md`](docs/operations/release-checklist.md);
- [`docs/operations/privacy-policy-template.md`](docs/operations/privacy-policy-template.md).

## Запуск TUI

Отдельный пошаговый гайд по установке готового бинарника и сборке из исходников
для Linux, macOS и Windows, локальному state-файлу, обновлению и диагностике:
[`docs/tui.md`](docs/tui.md).

Приватный moderation-клиент, bootstrap credential, SSH tunnel/Tailscale,
матрица ролей и риск сохранения раскрытого IP в scrollback описаны отдельно в
[`docs/admin-tui.md`](docs/admin-tui.md). Management listener нельзя направлять
через публичный listener или Caddy.

Встроенный пользовательский режим настраивается независимо:

```sh
BORD_ADMIN_MANAGEMENT=http://127.0.0.1:8081 \
BORD_SERVER=https://board.example.org \
bord-admin --credential-file ~/.config/bord/admin.credential
```

`--management`/`BORD_ADMIN_MANAGEMENT` используются только административным
клиентом, а `--server`/`BORD_SERVER` — только обычным public API. `F3` или
верхнее меню переключают «Администрирование»/«Пользователь». Выбранные режим и
тема сохраняются в `admin.json`; anonymous session и delete capabilities —
только в отдельном пользовательском `state.json`.

Минимальная команда для публичного сервера:

```sh
bord --server https://board.example.org
```

Все параметры:

```text
--server URL       адрес backend; иначе BORD_SERVER, затем сохранённое значение
--config PATH      путь к локальному state.json
--theme NAME|PATH  встроенная тема, имя или путь к MC skin .ini
--inline MODE      auto, kitty, sixel, ansi, w3m или off
--timeout DURATION HTTP timeout, по умолчанию 15s
--version          версия клиента
```

Примеры:

```sh
# Linux/macOS
BORD_SERVER=https://board.example.org bord --theme amber --inline auto

# Windows Terminal (PowerShell)
$env:BORD_SERVER = "https://board.example.org"
.\bord.exe --theme classic --inline off
```

Без `--config` state хранится в пользовательском config directory ОС. В нём
находятся последний раздел, тема, session secret и delete secrets. На Unix файл
создаётся с режимом `0600`. Не публикуйте его и не синхронизируйте через
незащищённое облако: потеря файла означает потерю возможности удалить свои
публикации, утечка — передачу этой возможности другому лицу.

`--inline=auto` проверяет возможности терминала и выбирает встроенный renderer в
порядке Kitty placeholders → Sixel → ANSI. Изображение остаётся частью экрана
вложения: `Esc` возвращает к посту, а `Enter` явно открывает проверенный файл
системным приложением. `--inline=kitty` и `--inline=sixel` запрашивают конкретный
протокол, но безопасно переходят на ANSI, если терминал его не подтвердил.
`--inline=ansi` всегда использует цветные half-block ячейки. `w3m` запускается
только по явной команде `O`, а `off` оставляет только явное внешнее открытие.
Системный opener использует `xdg-open`, `open` или Windows file association без
shell. Kitty, актуальные iTerm2 и WezTerm поддерживают Kitty placeholders;
Windows Terminal использует Sixel.

## Ретро landing

Статический landing находится в [`web/landing`](web/landing). Он не требует
Node.js, frontend framework или внешних CDN. В `web/landing/config.js` намеренно
оставлены пустыми `boardURL` и `downloadBaseURL`; после выбора доменов укажите
полные HTTPS URL и актуальную версию TUI.

Caddy отдаёт landing на `/`, release-архивы из `/srv/bord/downloads` на
`/downloads/*`, а API и health probes проксирует `bord-server`. Локальная
проверка файлов:

```sh
python3 scripts/validate-landing.py
```

## Управление TUI

### Каталог

- `↑`/`k`, `↓`/`j` — выбор строки;
- `Page Up` / `Page Down` — переход на страницу тредов;
- `Tab` — сменить активную панель;
- `Enter` — открыть доску или тред;
- `n` / `F7 New Thread` — создать тред;
- `r` / `F5` — обновить;
- `/` / `F4` — поиск.

### Открытый тред

- `p` / `F7 Reply Thread` — ответить в тред без адресации посту;
- `P` / `F8 Reply Post` — ответить на выделенный пост;
- `Enter` или один щелчок мышью по посту — открыть полный текст;
- `↑` / `↓` или колесо — прокрутить полный текст поста;
- `Page Up` / `Page Down` — переход на страницу постов;
- `Esc` — вернуться в каталог;
- `o` — открыть вложение выбранного поста.

Редактор всегда показывает цель: новый тред, текущий тред или конкретный
`>>post_id`. `Ctrl+S` отправляет, `Ctrl+O` прикрепляет JPEG/PNG, `Esc` возвращает
назад, сохраняя черновик. При сетевой ошибке повторный `Ctrl+S` использует тот же
idempotency key и не создаёт дубликат.

При ответе на конкретный пост редактор разделён на панели ответа и исходного
поста. `Tab` переключает панели, `Shift`+стрелки или drag мышью выделяют текст,
а `Ctrl+C` в панели исходного поста копирует выделение через OSC52.

### Общие команды

- `t` / `F2` — выпадающий список встроенных и найденных MC-тем;
- `s` / `F6` — меню настроек; пункт «Изображения» открывает отдельные варианты
  Auto, Kitty, Sixel, ANSI, `w3m` и System;
- `?` / `F1` — справка;
- `q`, `Ctrl+C` или `F10` — выход;
- колесо мыши — прокрутка панели под указателем;
- один клик по треду или посту — открыть для чтения.

MC skins автоматически находятся в стандартных XDG, пользовательских и
системных каталогах Midnight Commander. Можно также передать прямой путь к
`.ini` через `--theme`; выбранная тема сохраняется в локальном state.

## Обновление и резервное копирование

Перед заменой backend остановите запись и создайте проверяемый backup:

```sh
sudo systemctl stop bord.service
sudo -u bord bord-server backup \
  --offline \
  --database /var/lib/bord/bord.db \
  --blobs /var/lib/bord/blobs \
  --output /var/lib/bord/backups/pre-upgrade
sudo -u bord bord-server restore-verify \
  --backup /var/lib/bord/backups/pre-upgrade
sudo install -m 0755 dist/bord-server /usr/bin/bord-server
sudo -u bord bord-server migrate --database /var/lib/bord/bord.db
sudo systemctl start bord.service
curl --fail http://127.0.0.1:8080/readyz
```

Exit code `3` команды `status`/`migrate` означает, что схема базы новее
бинарника. Не запускайте старую версию поверх такой базы. Подробные процедуры
restore, retention, admin lifecycle и recovery находятся в
[`docs/operations/runbook.md`](docs/operations/runbook.md).

## Диагностика

### Backend не запускается

```sh
sudo journalctl -u bord.service -n 100 --no-pager
sudo -u bord bord-server status --database /var/lib/bord/bord.db
sudo ss -lntp | grep -E ':8080|:8081'
```

Частые причины:

- lookup secret отсутствует, не является base64 или короче 32 байт;
- включён `store_raw_ip`, но encryption key отсутствует или имеет неверный размер;
- каталог базы/blobs недоступен пользователю `bord`;
- база создана более новой версией приложения;
- listener уже занят другим процессом.

### TUI не подключается

```sh
curl --fail https://board.example.org/readyz
curl --fail https://board.example.org/api/v1/boards
bord --server https://board.example.org --inline off
```

Проверьте URL-схему (`https://`), сертификат, firewall и системное время. Если
цвета или рамки отображаются неверно, используйте современный UTF-8 терминал и
попробуйте `--theme mono`. При проблемах с изображениями запустите с
`--inline off`.

### SSE не обновляет открытый тред

Проверьте поток без TUI:

```sh
curl -N https://board.example.org/api/v1/events
```

Соединение должно оставаться открытым и получать heartbeat. Если события
буферизуются, проверьте, что Caddy использует предоставленный отдельный matcher
для `/api/v1/events`, а промежуточный CDN не буферизует SSE.

## Разработка

```sh
make build
make test
make test-race
make lint
make contract
make terminal-smoke
```

OpenAPI-контракт находится в `api/openapi.yaml`. `make contract` повторно
генерирует HTTP-клиент и sqlc adapter и завершается ошибкой при незакоммиченном
drift. Основные документы для операторов собраны в
[`docs/operations/README.md`](docs/operations/README.md).

## Участие в проекте

Перед issue или pull request ознакомьтесь с
[`CONTRIBUTING.md`](CONTRIBUTING.md), [`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md)
и [`SECURITY.md`](SECURITY.md). Уязвимости нельзя публиковать в обычных issues —
используйте GitHub private vulnerability reporting.
