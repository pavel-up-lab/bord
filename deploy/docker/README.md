# Docker-развёртывание за HTTPS edge

Этот compose-стек не публикует backend или внутренний Caddy на host-портах.
Edge-контейнер подключается к существующей внешней Docker-сети
`dom-kultury_default` (переопределяется через `BORD_EDGE_NETWORK`) под alias
`bord-edge`. Публичный HTTPS reverse proxy должен направлять домен на
`bord-edge:8088`.

Перед запуском рядом с `compose.yaml` должны находиться:

- собранный Linux-бинарник `bord-server`;
- `landing/` с файлами из `web/landing/`;
- `downloads/` с release-архивами из `dist-tui/`;
- `secrets/ip_lookup_key` с base64 от 32 случайных байт;
- `data/blobs/`, доступный UID/GID `65532`.

```sh
docker compose config --quiet
docker compose build backend
docker compose up -d
docker exec <public-caddy-container> wget -qO- http://bord-edge:8088/readyz
```

Внутренний Caddy доверяет `X-Forwarded-For` только от CIDR внешней edge-сети
(`BORD_UPSTREAM_PROXY_CIDRS`, по умолчанию `172.18.0.0/16`). Не публикуйте его
порт напрямую в интернет.
