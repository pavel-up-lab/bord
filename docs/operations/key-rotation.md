# Ротация ключа шифрования raw-IP

Lookup/HMAC-ключ не ротируется этой процедурой. Для encryption key выберите
новый неповторяющийся `key_id` (без значения ключа в ticket/log), остановите
сервис и выполните:

```sh
sudo -u bord bord-server ip-reencrypt --offline --database /var/lib/bord/bord.db --blobs /var/lib/bord/blobs --backup /var/lib/bord/backups/pre-key-rotation-20261017 --from-key-id primary --from-key-file /etc/bord/secrets/ip_encryption_key --to-key-id 2026-10 --to-key-file /etc/bord/secrets/ip_encryption_key_next --batch-size 100 --confirm REENCRYPT
```

Команда читает только base64-файлы с режимом `0600`, не принимает ключи в argv
и не печатает их. Каждый batch — отдельная транзакция; повтор команды пропускает
строки с новым `key_id`. Ошибка decrypt откатывает текущий batch. Backup
создается и проверяется до первой записи, а существующий каталог backup приводит
к отказу.

После результата с `Remaining:0` атомарно замените credential-файл, установите
`BORD_IP_ENCRYPTION_KEY_ID=2026-10` и запустите сервис. Во время поэтапного
развертывания можно дополнительно задать
`BORD_IP_PREVIOUS_ENCRYPTION_KEY_ID=primary` и
`BORD_IP_PREVIOUS_ENCRYPTION_KEY_FILE=...`; после завершения удалите прежний
ключ. Readiness fail-closed, если в БД остается неизвестный `key_id`.

При прерывании не меняйте идентификаторы/файлы, создайте новый путь backup и
повторите команду. При ошибке ключа восстановите проверенный backup в новый
каталог; не пытайтесь исправлять ciphertext вручную.
