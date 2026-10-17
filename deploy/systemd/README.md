# systemd-развертывание

Создайте системного пользователя `bord`, каталоги `/etc/bord/secrets` (`0700`) и
`/var/lib/bord` (`0700`, владелец `bord:bord`). Файлы credentials должны иметь
режим `0600`, владельца `root:root` и содержать по одному base64-значению без
кавычек. Unit передает их через изолированный systemd credential directory; они
не попадают в environment-файл или командную строку.

Скопируйте unit в `/etc/systemd/system/bord.service`, а примеры конфигурации — в
`/etc/bord`. Перед включением выполните команды из `docs/operations/install.md`.
