# Контракты security-moderation lane

Пакет не хранит и не журналирует исходные IP, bearer credentials, session/delete
secrets или расшифрованные значения. HTTP-адаптер обязан передавать в логи только
необратимый request ID и безопасный код результата.

## Интеграция с хранилищем

- `security.AdminIdentityRepository` хранит только Argon2id verifier, роль, срок
  действия и отметку отзыва. Bearer возвращается оператору один раз.
- `moderation.AuditRepository` предоставляет только `Append` и `List`. SQLite-
  адаптер не должен экспортировать update/delete и должен записывать результат
  административного действия в одной транзакции с самой мутацией.
- `moderation.ReportRepository.AddIfNew` обеспечивает уникальность пары
  `(post_id, source_fingerprint)`; повтор не повышает приоритет.
- `moderation.IPSubjectRepository` возвращает только AES-GCM envelope. Raw IP
  существует лишь в памяти на время явно разрешенной операции reveal.
- `moderation.RetentionRepository.DeleteExpiredIP` удаляет ciphertext и lookup
  identifiers на границе retention, исключая legal hold и действующую ban.

In-memory реализации предназначены для unit/adapter tests. Production adapters
должны сохранять описанные атомарность, уникальность и append-only ограничения.

## Ключи и ротация

Ключи lookup HMAC, шифрования IP, thread-author HMAC, PoW и admin credentials
имеют разные назначения и не переиспользуются. IP envelope содержит `key_id`;
keyring пишет primary key и читает все еще разрешенные старые ключи. После
завершения retention/ротации старый ключ удаляется из keyring.
