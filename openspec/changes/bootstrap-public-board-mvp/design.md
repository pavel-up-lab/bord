# Design

## Context

Проект создается с нуля; мотивация и продуктовые границы описаны в `proposal.md`, наблюдаемое поведение — в capability-спецификациях этого change. Сервер публичный и принимает недоверенный текст и изображения, поэтому безопасность, ограничение ресурсов, модерация и удаление чувствительных данных входят в базовую архитектуру, а не откладываются «на потом».

Клиент работает в современных терминалах Linux, macOS и Windows. Основной сценарий — клавиатурный TUI; графические протоколы терминалов являются необязательным улучшением. Реализация будет вестись несколькими Orca-воркерами, поэтому общие контракты должны стабилизироваться до параллельного написания модулей.

## Goals / Non-Goals

**Goals:**

- два независимо собираемых Go-бинарника с единым набором публичных контрактов;
- безопасный централизованный сервер для небольшой или средней публичной инсталляции;
- детерминированные границы пакетов, позволяющие параллельную разработку с минимальными конфликтами;
- чистый Go build без обязательного C toolchain для основных целей;
- контрактные, интеграционные и кроссплатформенные тесты до публичного запуска;
- путь миграции SQLite → PostgreSQL без преждевременной реализации второго хранилища.

**Non-Goals:**

- федерация, P2P, несколько активных серверных реплик и распределенный event bus;
- web-интерфейс, аккаунты, личные сообщения, полнотекстовый поиск и древовидные комментарии;
- плагины и пользовательские темы в MVP;
- видео, аудио, произвольные файлы и гарантированный inline-рендеринг во всех терминалах;
- старый `cmd.exe`, Windows Console Host и мобильные терминалы как поддерживаемые цели;
- автоматическое распознавание запрещенного контента и внешние CAPTCHA-провайдеры.

## Decisions

### 1. Go-монорепозиторий и модульный монолит

Используется один Go module и бинарники `cmd/bord`, `cmd/bord-server`. Сервер остается модульным монолитом: доменные пакеты не импортируют HTTP, SQLite или TUI, а зависимости направлены от адаптеров к домену.

Предлагаемая структура:

```text
cmd/bord/                  TUI-клиент
cmd/bord-server/           HTTP/SSE-сервер и admin-команды
internal/api/              HTTP handlers, middleware, DTO mapping
internal/app/              use cases и транзакционные границы
internal/domain/           сущности, политики, typed errors
internal/storage/sqlite/   sqlc queries и repository adapters
internal/media/            проверка, нормализация, blob store
internal/security/         IP protection, tokens, PoW
internal/events/           outbox и SSE fan-out
internal/tui/              Bubble Tea model/update/view
internal/client/           HTTP/SSE client library
internal/platform/         config, clock, logging, OS adapters
api/openapi.yaml           источник публичного HTTP-контракта
migrations/                встроенные SQL-миграции
deploy/                    Caddy, systemd, backup/restore
```

Go выбран вместо Rust ради скорости итераций и единого простого toolchain. Микросервисы отклонены: они усложнят транзакции, деплой и локальную разработку без пользы на масштабе MVP.

### 2. Контракт API стабилизируется первым

`api/openapi.yaml` определяет DTO, маршруты, лимиты и коды ошибок. На первом фундаментальном этапе создаются вручную поддерживаемые доменные типы и тонкие API DTO; OpenAPI используется для проверки контракта и генерации тестового клиента/схем, но сгенерированный код не должен проникать в домен.

Основные маршруты:

```text
GET    /api/v1/boards
GET    /api/v1/boards/{slug}/threads
POST   /api/v1/boards/{slug}/threads
GET    /api/v1/threads/{id}
GET    /api/v1/threads/{id}/posts
POST   /api/v1/threads/{id}/posts
DELETE /api/v1/posts/{id}
POST   /api/v1/posts/{id}/reports
POST   /api/v1/attachments
GET    /api/v1/attachments/{id}
GET    /api/v1/events?board={slug}|thread={id}
GET    /healthz
GET    /readyz
```

Создание использует `Idempotency-Key`; ответы ошибок имеют `code`, `message`, `request_id`, опциональные `fields` и `retry_after`. Неизвестные JSON-поля на write-маршрутах отклоняются. Курсор подписывается HMAC и включает тип коллекции, позицию и срок действия.

GraphQL отклонен из-за лишней сложности, WebSocket — потому что MVP требует в основном однонаправленные уведомления. HTTP остается источником состояния, SSE лишь сообщает об изменениях.

### 3. SQLite WAL с явным портом хранилища

MVP использует SQLite в WAL-режиме, `busy_timeout`, включенные foreign keys и короткие write-транзакции. Драйвер — pure-Go SQLite; запросы типизируются через `sqlc`, миграции встраиваются в бинарник и применяются явно командой или при разрешенном оператором startup-режиме.

Основные таблицы:

- `boards`, `threads`, `posts`, `attachments`;
- `anonymous_sessions`, `post_delete_tokens`;
- `idempotency_keys`, `event_outbox`;
- `reports`, `bans`, `moderation_actions`, `admin_identities`;
- `request_subjects` для ограниченных по сроку IP-данных;
- `schema_migrations`.

Текст, статус публикации и связь с вложением изменяются в одной транзакции. Вложение сначала пишется во временный файл, затем атомарно перемещается в content-addressed каталог; запись БД становится видимой только после успешного размещения. Периодическая reconciliation-задача удаляет осиротевшие временные/непривязанные blobs.

Интерфейсы репозиториев выражают use cases, а не копируют таблицы. Это оставляет путь к PostgreSQL, но второй адаптер в MVP не создается. ORM отклонен в пользу видимого SQL и предсказуемых запросов.

### 4. Транзакционный outbox для SSE

Каждое видимое изменение добавляет запись `event_outbox` в той же транзакции. Один внутрипроцессный dispatcher читает новые записи, будит подписчиков и хранит ограниченное окно для `Last-Event-ID`. Событие содержит версию схемы, тип, event id и идентификаторы ресурсов, но не полный текст поста: клиент перечитывает данные через HTTP.

Это устраняет окно «данные записались, событие потерялось». Полноценный broker отклонен; при переходе к нескольким репликам outbox можно публиковать во внешний transport без смены публичного SSE-контракта.

### 5. Анонимная сессия и секреты удаления

Клиент генерирует 256-битный session secret и отдельный 256-битный delete secret для каждой публикации. На сервер передаются производные идентификаторы; секрет удаления хранится только у клиента, сервер хранит Argon2id verifier с версией параметров. Авторский ID треда вычисляется как усеченный HMAC серверным ключом от `(session fingerprint, thread id)`.

Локальные секреты сохраняются атомарно в пользовательском config directory. На Unix устанавливаются права `0600`; на Windows используются пользовательские директории и запрет вывода секретов, а интеграция с Credential Manager оставляется после MVP. Потеря файла означает потерю права удаления.

### 6. IP-данные разделены по назначению

Remote address определяется только из соединения либо из proxy-заголовка, если непосредственный peer входит в настроенный список trusted proxies. IP канонизируется как 16 байт.

- `ip_lookup = HMAC-SHA256(lookup_key, canonical_ip)` используется для лимитов и поиска;
- исходный IP при включенной политике шифруется AES-256-GCM отдельным ключом и уникальным nonce;
- записи содержат `key_id`, что позволяет ротацию;
- приложение никогда не пишет raw IP в обычный лог;
- фоновая retention-задача по умолчанию удаляет защищенные IP через 90 дней.

Полное необратимое хэширование отклонено, потому что модератору может потребоваться разрешенное раскрытие; хранение plaintext отклонено как неоправданный риск. Правовой текст privacy policy не генерируется автоматически, но оператор обязан опубликовать политику до публичного запуска.

### 7. Anti-abuse pipeline до доменной записи

Write-запрос проходит: size limit → parse/validation → subject derivation → ban check → token-bucket limits → при необходимости PoW → доменный use case. Лимиты существуют для IP HMAC, префикса, session fingerprint и операции. Значения конфигурируются, но безопасные defaults входят в пример конфигурации.

PoW challenge подписан сервером, содержит назначение, subject, сложность, expiry и nonce. Решения одноразовые; использованные challenge фиксируются до expiry. Сложность ограничена сервером, чтобы не зависать на слабых клиентах. На старте challenge включается после превышения мягкого порога, а не для каждого поста.

### 8. Медиа: JPEG и PNG, нормализация pure Go

MVP принимает JPEG и PNG до настраиваемого лимита. Сервер проверяет magic bytes, полностью декодирует изображение с лимитом пикселей, удаляет метаданные путем повторного кодирования и сохраняет нормализованный файл. Имя blob строится из SHA-256, публичный ID остается непрозрачным. Пользовательское имя очищается и используется только как download metadata.

GIF/WebP и антивирусный daemon не входят в MVP: первый усложняет безопасное перекодирование, второй вводит обязательную внешнюю службу. Архитектура media scanner допускает добавление новых decoder/scan stages позже.

Клиент скачивает файл во временный cache, проверяет ожидаемый SHA-256 и затем либо передает renderer, либо запускает известную системную команду напрямую без shell: `xdg-open`, `open` или Windows ShellExecute-адаптер. Первый inline renderer — Kitty graphics; Sixel добавляется как изолированный адаптер после проверки на целевых терминалах. Автоопределение можно переопределить конфигурацией.

### 9. TUI как конечный автомат

Bubble Tea управляет единой immutable-style моделью экранов: boards, catalog, thread, composer, help и attachment. Сетевые операции выполняются командами и возвращают типизированные сообщения; `Update` не блокируется. `internal/client` не импортирует TUI и тестируется отдельно.

Компоновка использует две панели при достаточной ширине и стек экранов при узком окне. Keymap описывается централизованно и отображается контекстной справкой. Draft хранится в памяти до успеха отправки; idempotency key сохраняется на все повторы одной отправки. SSE consumer имеет backoff с jitter и вызывает HTTP reconciliation после `stream.reset`.

### 10. Администрирование без отдельного web UI

`bord-server admin ...` предоставляет команды создания/отзыва администратора, списка жалоб, hide/restore, close и ban. Online-операции идут через отдельные `/admin/v1` endpoints, которые можно привязать к loopback/management listener. Высокоэнтропийные bearer credentials хранятся сервером как Argon2id verifier, имеют роль и срок, передаются только по защищенному каналу.

Прямое редактирование SQLite административной CLI отклонено: оно обходит аудит и доменные инварианты. Каждая попытка административной операции создает append-only audit row; изменение существующей audit row запрещается API и repository.

### 11. Конфигурация и эксплуатация

Конфигурация имеет приоритет defaults → YAML file → environment; секреты разрешены только через environment или отдельные secret files. `slog` пишет JSON на сервере и человекочитаемый формат в интерактивном клиенте. Request ID создается на edge приложения или валидируется из доверенного proxy.

Caddy завершает TLS, ограничивает тела и отключает buffering для SSE. Сервер поставляется с hardened systemd unit. Backup использует SQLite online backup/VACUUM INTO под координацией приложения, затем согласованно копирует blobs и manifest с хэшами. Restore всегда проверяется отдельной командой до замены рабочей директории.

### 12. Стратегия тестирования

- unit tests: доменные политики, курсоры, crypto envelope, PoW, keymap и reducers;
- repository tests: временная SQLite с реальными миграциями;
- API contract tests: каждый OpenAPI response и error code;
- integration tests: create/read/delete, outbox/SSE resume, media cleanup, bans и retention;
- golden tests: TUI для заданных размеров и цветовых профилей;
- platform tests: Linux/macOS/Windows build matrix и opener adapters;
- security tests: fuzzing JSON/cursor/media parsers, race detector, dependency/vulnerability scan;
- smoke restore test: запуск сервера на восстановленной копии и проверка хэшей.

Тесты используют injectable clock, random source и filesystem boundary; production randomness никогда не заменяется детерминированной реализацией вне тестов.

### 13. Параллельная реализация через Orca worktree

Работа идет волнами, а не всеми ветками сразу:

1. **Foundation** в отдельном worktree: Go module, CI, package skeleton, OpenAPI, error model, config contracts и migration conventions. После review этот слой вливается в `main`.
2. От обновленного `main` параллельно создаются worktree с непересекающимся владением:
   - **storage-domain** — `internal/domain`, `internal/app`, `internal/storage/sqlite`, `migrations`;
   - **http-realtime** — `internal/api`, `internal/events`, серверная композиция, contract tests;
   - **security-moderation** — `internal/security`, moderation use cases и admin commands;
   - **media** — `internal/media`, attachment repository adapter и media tests;
   - **tui-client** — `internal/client`, `internal/tui`, `cmd/bord`;
   - **operations** — `deploy`, release workflow, backup/restore и smoke tests.
3. Воркеры не меняют `api/openapi.yaml`, общие интерфейсы или миграции вне своей зоны без сообщения координатору. Изменение контракта сначала оформляется отдельным небольшим интеграционным commit.
4. Слияние идет в порядке: storage-domain → security/media → http-realtime → tui-client → operations. После каждой группы запускаются contract и integration gates.
5. Финальный **integration-hardening** worktree владеет только сквозными исправлениями, race/fuzz результатами и release checklist.

Каждый Orca prompt должен ссылаться на этот change, назначенный диапазон задач и список owned paths. Worktree создаются как независимые дочерние от актуального `main`, не как цепочка feature-веток; исключение — явно обозначенная зависимая задача.

## Risks / Trade-offs

- [SQLite сериализует запись и ограничивает горизонтальное масштабирование] → короткие транзакции, WAL, нагрузочные пороги и документированный trigger миграции на PostgreSQL.
- [SSE через proxy может буферизоваться или обрываться] → Caddy-конфигурация, heartbeat, resume и HTTP reconciliation.
- [HMAC IP остается персональными/псевдонимизированными данными] → разделение ключей, retention, минимальный доступ и privacy policy оператора.
- [Статический management credential может утечь] → высокий entropy, Argon2id verifier, expiry/revocation, отдельный listener и аудит; OIDC остается будущим улучшением.
- [Перекодирование изображений расходует CPU/RAM] → ограничения compressed bytes, dimensions и pixels до публикации, таймаут и bounded workers.
- [Inline graphics различаются между терминалами] → capability adapters, явное отключение и гарантированный external-opener fallback.
- [Параллельные worktree могут разойтись по контрактам] → foundation gate, owned paths, contract tests и интеграционные commits через координатора.
- [Pure-Go SQLite может уступать C-драйверу] → benchmark до публичного запуска; смена драйвера остается внутренним решением repository adapter.

## Migration Plan

1. Собрать foundation и применить начальную схему к новой пустой директории данных.
2. Развернуть сервер за Caddy в закрытом staging, создать первого администратора и тестовые доски.
3. Выполнить contract, integration, abuse, backup/restore и cross-platform client smoke tests.
4. Запустить ограниченную публичную beta с консервативными лимитами и включенным аудитом, но без inline graphics по умолчанию.
5. После наблюдения за latency, SQLite busy rate, SSE reconnects и moderation load включать дополнительные renderers и корректировать лимиты.

Rollback до появления пользовательских данных выполняется заменой бинарника и директории данных. После публичного запуска rollback бинарника разрешен только к версии, поддерживающей текущую схему; destructive down-migrations не используются. Перед обновлением создается проверенная резервная копия, а несовместимый бинарник не проходит readiness.
