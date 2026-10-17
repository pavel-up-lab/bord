# Tasks

## Execution map

Реализация запускается волнами. `foundation` выполняется и вливается первым. После него `storage-domain`, `security-moderation`, `media`, `http-realtime`, `tui-client` и `operations` создаются как независимые дочерние Orca worktree от обновленного `main`. Внутри каждой lane задачи идут по порядку; между lane допустима параллельная работа. `integration-hardening` стартует после слияния всех lane.

| Orca worktree | Task groups | Owned paths | Hard dependencies |
|---|---:|---|---|
| `bord-foundation` | 1 | `go.*`, `api/`, shared tooling, package skeleton | нет |
| `bord-storage-domain` | 2 | `internal/domain`, `internal/app/board`, `internal/storage/sqlite`, `migrations` | group 1 |
| `bord-security-moderation` | 3 | `internal/security`, `internal/app/moderation`, `internal/admin` | group 1; интеграция с group 2 |
| `bord-media` | 4 | `internal/media`, `internal/app/attachment` | group 1; интеграция с group 2 |
| `bord-http-realtime` | 5 | `internal/api`, `internal/events`, `internal/server`, `cmd/bord-server` кроме admin package | group 1; mock ports до groups 2–4 |
| `bord-tui-client` | 6 | `internal/client`, `internal/tui`, `cmd/bord` | group 1; OpenAPI contract |
| `bord-operations` | 7 | `deploy`, `.github`, `docs/operations`, release scripts | group 1; smoke hooks после group 5 |
| `bord-integration-hardening` | 8 | только сквозные исправления и release checklist | groups 2–7 |

Воркеры не меняют `api/openapi.yaml`, общие интерфейсы foundation или чужие owned paths без согласованного интеграционного commit. Каждый worker перед завершением обновляет свою ветку от актуального `main`, запускает указанные проверки и передает координатору commit/PR с перечнем затронутых контрактов.

## 1. Foundation gate — `bord-foundation`

- [x] 1.1 Инициализировать Go module, зафиксировать поддерживаемую версию Go, добавить `.gitignore`, `LICENSE`, базовый `README.md` и пустые entrypoints `cmd/bord`/`cmd/bord-server`; проверить `go build ./...`.
- [x] 1.2 Добавить Makefile или Taskfile с командами `fmt`, `lint`, `test`, `test-race`, `generate`, `contract` и `build`; проверить запуск локальных команд на чистом checkout.
- [x] 1.3 Подключить минимальные зависимости и package skeleton из `design.md`, документировать правило направленности импортов; проверить architecture test, запрещающий импорт adapters из domain/app.
- [x] 1.4 Описать OpenAPI 3 контракт `/api/v1`, `/admin/v1`, health/readiness, все DTO, error codes, pagination, idempotency и лимиты; проверить schema lint и генерацию тестового клиента.
- [x] 1.5 Добавить общие typed IDs, domain error taxonomy и API error mapping contracts без бизнес-реализации; проверить table-driven tests для стабильных кодов ошибок.
- [x] 1.6 Реализовать общие порты `Clock`, `Random`, транзакции, repositories, blob store и event publisher с compile-time mocks/fakes; проверить сборку всех будущих adapter packages против интерфейсов.
- [x] 1.7 Реализовать загрузку defaults → YAML → environment, secret-file references и валидацию обязательных секретов; проверить unit tests приоритетов и отказа при отсутствующем ключе IP encryption.
- [x] 1.8 Добавить безопасный `slog` setup, request/correlation ID типы и redaction helpers; проверить тестом, что raw IP, post body, bearer и delete token не попадают в сериализованный лог.
- [x] 1.9 Настроить `sqlc`, инструмент миграций и embed conventions на минимальной тестовой миграции; проверить создание временной SQLite, upgrade и отказ на неизвестной новой версии схемы.
- [x] 1.10 Настроить CI foundation gate для format, vet/lint, unit tests, OpenAPI lint и Linux/macOS/Windows compile; проверить успешный workflow на текущем skeleton.
- [x] 1.11 Провести review/freeze foundation contracts, исправить замечания и влить worktree в `main`; проверить, что все шесть parallel lane создаются от одного commit `main` и `git status` чист.

## 2. Storage and board domain — `bord-storage-domain`

- [x] 2.1 Реализовать domain entities и инварианты boards, threads, posts, tombstones и thread-local author IDs; проверить unit tests создания, закрытого треда, cross-thread reply и tombstone.
- [x] 2.2 Создать начальные SQLite migrations для boards, threads, posts, sessions, delete verifiers, idempotency, attachments, outbox, reports, bans, audit и request subjects; проверить upgrade пустой БД и foreign-key integrity.
- [x] 2.3 Реализовать sqlc queries и repositories для списка досок, курсорного каталога и последовательного чтения треда; проверить repository tests на стабильный порядок, границы страниц и неизвестные ресурсы.
- [x] 2.4 Реализовать транзакционные use cases создания треда и ответа с валидацией `reply_to_id`; проверить integration tests атомарности и отказа пустой публикации.
- [x] 2.5 Реализовать idempotency repository и семантику повторов create-операций; проверить, что идентичный повтор возвращает исходный ресурс, а измененный payload дает conflict.
- [x] 2.6 Реализовать хранение verifier токена удаления и use case owner delete с tombstone; проверить успешное удаление, неверный token и отсутствие исходного содержимого в read model.
- [x] 2.7 Добавить запись versioned outbox event в той же транзакции, что content mutation; проверить rollback обоих изменений при injected failure.
- [x] 2.8 Реализовать SQLite runtime settings WAL, foreign keys, busy timeout, pool limits и readiness probe; проверить конкурентным тестом bounded writes без `database is locked` при целевой нагрузке теста.
- [x] 2.9 Документировать repository contracts и пороговые метрики перехода на PostgreSQL рядом с storage package; проверить, что package examples и все storage tests проходят.

## 3. Security, abuse controls and moderation — `bord-security-moderation`

- [x] 3.1 Реализовать генерацию session/delete secrets, session fingerprint, thread-local author HMAC и Argon2id verifier с версией параметров; проверить криптографические test vectors и отрицательные случаи.
- [x] 3.2 Реализовать trusted-proxy parser, canonical IP, prefix derivation, HMAC lookup и AES-256-GCM envelope с `key_id`; проверить spoofed forwarded headers, IPv4/IPv6 vectors, tamper detection и key rotation read path.
- [x] 3.3 Реализовать token-bucket limits по IP lookup, prefix, session и operation с injectable clock; проверить burst, refill, независимость read/write и `retry_after`.
- [x] 3.4 Реализовать подписанный expiring PoW challenge и ограниченный solver library для клиента; проверить purpose binding, expiry, difficulty bounds и запрет повторного использования.
- [x] 3.5 Реализовать ban evaluation по IP, prefix, lookup, board, operation и expiry; проверить приоритет совпадений и автоматическое прекращение действия после expiry.
- [x] 3.6 Реализовать reports use case с допустимыми причинами, дедупликацией и лимитами; проверить, что повторный источник не увеличивает приоритет жалобы.
- [x] 3.7 Реализовать admin identities/roles, Argon2id bearer verifier, expiry/revocation и constant-time verification; проверить unauthorized, expired, revoked и role-denied cases.
- [x] 3.8 Реализовать moderation use cases hide/restore, close thread, create/revoke ban и reveal IP с обязательной причиной; проверить доменные эффекты и запрет reveal для недостаточной роли.
- [x] 3.9 Реализовать append-only audit repository для успешных и неуспешных admin actions; проверить невозможность update/delete через public repository и отсутствие secrets/raw IP в audit payload.
- [x] 3.10 Реализовать retention job для encrypted IP, idempotency и использованных challenge с legal-hold/active-ban исключениями; проверить default 90-day boundary с fake clock.
- [x] 3.11 Добавить `internal/admin` command handlers для bootstrap/revoke/list-reports/hide/restore/close/ban/reveal без прямого изменения БД; проверить CLI handler tests через mock admin client.

## 4. Media pipeline — `bord-media`

- [x] 4.1 Реализовать streaming intake с ограничением compressed bytes, sniffing JPEG/PNG, проверкой dimensions/pixel count и безопасным filename; проверить valid samples, polyglot/invalid content и decompression bomb fixtures.
- [x] 4.2 Реализовать полное decode/re-encode JPEG/PNG без EXIF и вычисление SHA-256 нормализованного файла; проверить отсутствие тестовых EXIF/GPS markers и стабильность metadata.
- [x] 4.3 Реализовать filesystem blob store с temp write, fsync по политике, atomic rename и content-addressed layout; проверить injected write/rename failures без видимого частичного blob.
- [x] 4.4 Реализовать attachment use cases prepare/attach/fetch/delete и транзакционное согласование с post repository; проверить, что один пост не получает два вложения, а orphan не становится публичным.
- [x] 4.5 Реализовать reconciliation/cleanup временных, orphaned и удаленных blobs с безопасным grace period; проверить dry-run отчет и фактическую очистку на fixture store.
- [x] 4.6 Добавить media fuzz targets и resource-bound tests; проверить установленное ограничение времени/памяти в CI-подходящем corpus run.

## 5. HTTP API and realtime — `bord-http-realtime`

- [x] 5.1 Собрать HTTP router и middleware order: recovery, request ID, trusted proxy, body limit, structured access log, security headers и timeout policy; проверить middleware integration tests и отсутствие secret fields в логах.
- [x] 5.2 Реализовать strict JSON decoding, OpenAPI DTO mapping, uniform errors, signed cursors и `Idempotency-Key`; проверить contract tests всех error envelopes и поврежденных курсоров.
- [x] 5.3 Реализовать public handlers boards/catalog/thread/create/reply/delete через app ports; проверить OpenAPI conformance и table-driven HTTP tests на success/not-found/closed/validation cases.
- [x] 5.4 Реализовать attachment upload/download handlers с ранним body limit, `nosniff`, безопасным disposition и недоступностью удаленного blob; проверить HTTP media integration fixtures.
- [x] 5.5 Реализовать report и PoW challenge endpoints и включить anti-abuse pipeline перед write handlers; проверить HTTP 429/retry, challenge-required и accepted solution flows.
- [x] 5.6 Реализовать outbox dispatcher и bounded subscriber hub с медленным-клиентом policy; проверить порядок event IDs, отсутствие утечки goroutine и delivery после commit.
- [x] 5.7 Реализовать SSE endpoint, heartbeat, `Last-Event-ID`, retention replay и `stream.reset`; проверить reconnect tests через реальный `httptest.Server`.
- [x] 5.8 Реализовать `/admin/v1` handlers и отдельную management listener policy поверх moderation ports; проверить bearer roles, audit correlation и отсутствие admin routes на public listener при раздельной конфигурации.
- [x] 5.9 Реализовать `/healthz`, `/readyz`, graceful shutdown и композицию `cmd/bord-server`; проверить снятие readiness до остановки, завершение SSE и отказ readiness при storage/blob failure.
- [x] 5.10 Запустить полный generated-client contract suite против собранного сервера на fake ports и исправить только adapter-level расхождения без изменения frozen OpenAPI; проверить `make contract`.

## 6. Terminal client — `bord-tui-client`

- [x] 6.1 Реализовать типизированный HTTP client для boards, catalog, threads, create/delete, reports, attachments и uniform errors; проверить generated contract fixtures и cancellation/timeouts.
- [x] 6.2 Реализовать SSE client parser с heartbeat, unknown-event ignore, exponential backoff+jitter, resume ID и `stream.reset` callback; проверить fragmented stream, reconnect и cancellation tests.
- [x] 6.3 Реализовать кроссплатформенное атомарное local store для config, session secret, delete secrets и last board с Unix `0600`; проверить round-trip, corrupted file recovery и отсутствие secrets в error/log output.
- [x] 6.4 Реализовать root Bubble Tea model и состояния boards/catalog/thread/composer/help/attachment с неблокирующими commands; проверить reducer tests на переходы, stale responses и cancellation.
- [x] 6.5 Реализовать MC-подобную двухпанельную layout, focus, scrolling и однопанельный narrow fallback; проверить golden snapshots для нескольких размеров и color profiles.
- [x] 6.6 Реализовать единый keymap, контекстную help, keyboard-only navigation и graceful terminal restoration; проверить model tests всех обязательных действий и subprocess smoke test аварийного выхода.
- [x] 6.7 Реализовать thread view, ссылки `>>id`, tombstones, incremental pagination и SSE-triggered reconciliation; проверить сохранение selection/scroll и отсутствие дубликатов после reconnect.
- [x] 6.8 Реализовать multiline composer для thread/reply, attachment selection, idempotency reuse и сохранение draft при ошибке; проверить retry создает один пост и успешная отправка очищает draft.
- [x] 6.9 Реализовать attachment cache с SHA-256 verify и безопасные external opener adapters Linux/macOS/Windows без shell; проверить command construction unit tests и platform compile matrix.
- [x] 6.10 Реализовать capability interface и Kitty inline renderer с manual override/off; проверить escape-sequence golden tests и гарантированный fallback при неизвестном терминале.
- [x] 6.11 Собрать `cmd/bord`, CLI flags/config resolution и версию клиента; проверить `go build` для Linux/macOS/Windows и scripted TUI smoke flow с mock server.

## 7. Operations and release — `bord-operations`

- [x] 7.1 Подготовить production-ready Caddyfile с TLS, request limits, trusted proxy boundary и отключением buffering/подходящими timeout для SSE; проверить `caddy validate` и staging SSE smoke.
- [x] 7.2 Подготовить hardened systemd unit, environment/secret-file examples, data directory permissions и graceful stop timeout; проверить `systemd-analyze verify` и документированный fresh install.
- [x] 7.3 Реализовать server subcommands migrate/status и защиту от запуска на более новой схеме; проверить upgrade fixture и incompatible-schema exit code.
- [x] 7.4 Реализовать согласованный backup manifest для SQLite+blobs и offline restore verification с SHA-256; проверить end-to-end backup/restore на fixture data.
- [ ] 7.5 Документировать установку, конфигурацию, key rotation, retention, создание администратора, блокировки, recovery и обязательство privacy policy; проверить все команды документации в чистом staging environment.
- [x] 7.6 Настроить release workflow для checksummed архивов `bord` на Linux/macOS/Windows и `bord-server` на целевом Linux; проверить reproducible version metadata и установку каждого artifact.
- [ ] 7.7 Добавить dependency/vulnerability/license scanning и SBOM для release; проверить CI artifacts и отсутствие неразрешенной лицензии или известной critical vulnerability.
- [x] 7.8 Добавить operational metrics/log events для latency, SQLite busy, SSE reconnect/reset, media rejects, rate limits и retention без high-cardinality/raw IP labels; проверить telemetry schema tests.

## 8. Integration and hardening — `bord-integration-hardening`

- [x] 8.1 Влить lane в порядке storage-domain → security/media → http-realtime → tui-client → operations, разрешая конфликты через frozen contracts; проверить `go test ./...` и `make contract` после каждого merge.
- [x] 8.2 Провести сквозной сценарий create board seed → create thread with image → reply → SSE update → owner delete → tombstone → moderation audit; проверить автоматизированный E2E test на реальном SQLite/filesystem.
- [x] 8.3 Провести anti-abuse E2E для spoofed proxy, rate limits, PoW, bans, reports, IP reveal audit и 90-day retention; проверить ожидаемые HTTP/CLI результаты без утечки raw IP.
- [ ] 8.4 Запустить race detector, выбранные fuzz targets и goroutine leak tests для HTTP/SSE/media/client; устранить найденные дефекты и приложить успешные команды к release checklist.
- [ ] 8.5 Выполнить benchmark/load profile публичных read/write/SSE сценариев, зафиксировать MVP capacity и alerts для SQLite busy/latency; проверить достижение документированного минимального профиля либо записать blocking decision.
- [ ] 8.6 Выполнить Linux/macOS/Windows Terminal smoke matrix, включая narrow layout, external opener и graceful exit; зафиксировать поддержанные terminal capabilities и известные ограничения.
- [x] 8.7 Выполнить staging deployment за Caddy, upgrade, backup, destructive staging incident и restore; проверить readiness, data/blob hashes и rollback совместимого бинарника.
- [ ] 8.8 Провести security/privacy review входных лимитов, cryptography, secret handling, logs, admin boundary и retention; закрыть critical/high findings и сохранить отчет без секретных данных.
- [ ] 8.9 Завершить MVP release checklist с версиями toolchain, checksums, SBOM, migration version, operator runbook и beta defaults; проверить, что OpenSpec tasks, CI и staging gates зеленые.

## Workflow follow-up

- После review реализации синхронизировать delta specs с основными specs OpenSpec.
- После успешной публичной beta и выполнения всех tracked tasks архивировать change `bootstrap-public-board-mvp`.
- Отдельными будущими changes рассмотреть PostgreSQL, Sixel, WebP/GIF, OIDC для модераторов и web-клиент; они не блокируют MVP.
