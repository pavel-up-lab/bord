# Bord Admin TUI

`bord-admin` — отдельный приватный terminal-клиент для модераторов Bord. Он подключается только к management listener и не добавляет административный режим в публичный `bord`.

## Требования безопасности

- Management listener слушает loopback или приватный адрес. Не публикуйте `/admin/v1/*` через Caddy и публичный DNS.
- Credential передаётся через `BORD_ADMIN_CREDENTIAL` либо защищённый файл. У `bord-admin` намеренно нет флага со значением credential.
- На Linux/macOS credential-файл должен принадлежать текущему пользователю, быть обычным файлом (не symlink) и не иметь group/other permissions.
- Поддерживаются Linux, macOS и современный Windows Terminal. Старый `cmd.exe` не поддерживается. На Windows ограничьте ACL файла текущим пользователем.

## Bootstrap identity

Identity создаётся операторской командой сервера при остановленном процессе; TUI не умеет создавать или отзывать identities:

```sh
sudo systemctl stop bord.service
sudo -u bord bord-server admin create \
  --offline \
  --database /var/lib/bord/bord.db \
  --id moderator-1 \
  --role moderator \
  --ttl 720h \
  --credential-output /var/lib/bord/admin-moderator-1.credential
sudo chmod 600 /var/lib/bord/admin-moderator-1.credential
sudo systemctl start bord.service
```

Скопируйте файл оператору защищённым каналом и сохраните как `~/.config/bord/admin.credential` с правами `0600`. Не печатайте credential и не помещайте его в shell history.

## Приватное подключение

### SSH tunnel

```sh
ssh -N -L 18081:127.0.0.1:8081 admin@board.example.org
bord-admin --management http://127.0.0.1:18081
```

### Tailscale или другая приватная сеть

Ограничьте listener/firewall приватным интерфейсом и запустите:

```sh
bord-admin \
  --management http://100.64.0.10:8081 \
  --credential-file "$HOME/.config/bord/admin.credential"
```

Переменные окружения: `BORD_ADMIN_MANAGEMENT`, `BORD_ADMIN_CREDENTIAL_FILE` и `BORD_ADMIN_CREDENTIAL`. Последняя имеет наивысший приоритет, но credential-файл предпочтительнее для production. Локальный `admin.json` содержит только URL, тему, inline mode и последний безопасный экран — credential туда не записывается.

## Роли

| Возможность | reports | moderator | administrator |
|---|:---:|:---:|:---:|
| Очередь жалоб | ✓ | ✓ | ✓ |
| Hidden context и вложения | — | ✓ | ✓ |
| Hide/restore/close | — | ✓ | ✓ |
| Создать/отозвать ban | — | ✓ | ✓ |
| Аудит | — | ✓ | ✓ |
| Reveal IP | — | — | ✓ |

Сервер проверяет роль для каждого запроса; скрытые в UI команды не являются границей безопасности.

## Управление

- `1`, `2`, `3` — Жалобы, Блокировки, Аудит;
- `Tab` — переключить список/контекст в узком терминале;
- `↑/↓`, `PgUp/PgDn`, мышь — навигация;
- `H`, `U`, `C` — hide, restore, close;
- `B`, `X` — создать ban по посту, отозвать ban;
- `A` — открыть проверенное вложение; `I` — reveal IP для administrator;
- `F2` — тема/MC skin, `F5` — обновить, `F6` — active/history;
- `F1` — справка, `F10` — выход.

Hide/restore/close/ban/revoke/reveal требуют причины и повторного подтверждения. Показанный IP не копируется автоматически и очищается при закрытии modal, но терминальный эмулятор может сохранить его в scrollback. Выполняйте reveal только в доверенной сессии и очистите scrollback согласно политике терминала.

## Проверка установки

```sh
bord-admin --version
test "$(stat -c '%a' "$HOME/.config/bord/admin.credential")" = 600  # Linux
curl --fail http://127.0.0.1:18081/healthz
bord-admin --management http://127.0.0.1:18081 --inline off
```

Ошибка о несовместимом management API означает, что сервер не предоставляет `GET /admin/v1/session`; сначала обновите backend. Коды 401/403 означают недействительный credential или недостаточную роль, а не публичную доступность API.
