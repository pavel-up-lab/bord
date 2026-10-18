# Изолированное Docker-развёртывание

Этот compose-стек публикует Bord на отдельном HTTP-порту `8088` и не изменяет
существующий reverse proxy на 80/443. Он предназначен для временного запуска по
IP до назначения HTTPS-домена.

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
curl --fail http://127.0.0.1:8088/readyz
```

Публичный порт переопределяется переменной `BORD_PUBLIC_PORT`. Перед настоящим
публичным запуском перенесите маршрут на HTTPS reverse proxy и не используйте
нешифрованный HTTP для создания публикаций.
