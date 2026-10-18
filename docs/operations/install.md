# Установка и первичный запуск

## Предварительные условия

- Linux с systemd и Caddy 2.8 или новее;
- DNS-имя, указывающее на узел, и открытые TCP 80/443;
- отдельный пользователь `bord` без shell;
- опубликованная privacy policy до открытия публичного доступа.

## Чистая установка

```sh
sudo useradd --system --home-dir /var/lib/bord --shell /usr/sbin/nologin bord
sudo install -d -m 0700 -o bord -g bord /var/lib/bord /var/lib/bord/blobs
sudo install -d -m 0750 -o root -g bord /etc/bord
sudo install -d -m 0700 -o root -g root /etc/bord/secrets
sudo install -d -m 0755 -o root -g root /usr/share/bord/landing
sudo install -d -m 0755 -o root -g root /srv/bord/downloads
sudo install -m 0755 bord-server /usr/bin/bord-server
sudo install -m 0644 deploy/systemd/bord.service /etc/systemd/system/bord.service
sudo install -m 0640 -o root -g bord deploy/systemd/bord.env.example /etc/bord/bord.env
sudo install -m 0640 -o root -g bord deploy/systemd/config.yaml.example /etc/bord/config.yaml
sudo cp -a web/landing/. /usr/share/bord/landing/
sudo cp dist-tui/* /srv/bord/downloads/
```

В `/usr/share/bord/landing/config.js` оставлены пустые `boardURL` и
`downloadBaseURL`. После выбора доменов задайте их как полные HTTPS URL и
обновите `version` так, чтобы она совпадала с именами архивов. Пока значения
пусты, landing безопасно показывает placeholder и не создаёт нерабочие ссылки.

Создайте два **разных** 32-байтовых ключа и сохраните base64-представление, не
выводя его в историю команд или журналы. Подходящий путь — менеджер секретов,
который атомарно записывает `/etc/bord/secrets/ip_lookup_key` и
`ip_encryption_key` с режимом `0600`.

```sh
sudo -u bord /usr/bin/bord-server migrate --database /var/lib/bord/bord.db
sudo -u bord /usr/bin/bord-server status --database /var/lib/bord/bord.db
sudo -u bord /usr/bin/bord-server admin create --offline --database /var/lib/bord/bord.db --id root --role administrator --ttl 720h --credential-output /var/lib/bord/admin-root.credential
sudo systemd-analyze verify /etc/systemd/system/bord.service
sudo systemctl daemon-reload
sudo systemctl enable --now bord.service
```

Установите `deploy/Caddyfile` как `/etc/caddy/Caddyfile`, задайте
`BORD_DOMAIN`, оставьте upstream на loopback и при необходимости переопределите
`BORD_LANDING_ROOT`/`BORD_DOWNLOAD_ROOT`. Caddy отдаёт landing на `/`, архивы
на `/downloads/*`, а `/api/*`, `/healthz` и `/readyz` проксирует backend:

```sh
caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile
sudo systemctl reload caddy
curl --fail --silent https://board.example.org/healthz
curl --fail --silent https://board.example.org/readyz
```

Серверу разрешено доверять proxy-заголовкам только от loopback-адреса Caddy.
Если перед Caddy есть балансировщик, задайте его точные CIDR через
`BORD_UPSTREAM_PROXY_CIDRS`; не используйте все частные сети без необходимости.

## Локальная проверка чистого staging

`scripts/operator-staging-smoke.sh` в чистом временном каталоге проверяет все
offline-команды оператора, подтверждения, credential lifecycle, backup/restore,
retention и resumable re-encryption без печати секретов. Запустите его первым:

```sh
./scripts/operator-staging-smoke.sh
```

Основной скрипт создает только временные каталоги, поднимает настоящий Caddy с локальным
TLS, устанавливает две версии `bord-server`, проверяет SSE, upgrade, backup,
разрушение данных, restore и совместимый rollback. Caddy можно положить вне
репозитория и передать явно:

```sh
CADDY_BIN=/path/to/caddy ./scripts/staging-smoke.sh
```

Скрипт не подменяет проверку публичного DNS/ACME и systemd на целевом Linux, но
воспроизводимо проверяет команды и пути данных без root и без сохранения секретов.
