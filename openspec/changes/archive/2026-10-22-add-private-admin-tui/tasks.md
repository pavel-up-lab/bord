# Tasks

## 1. Management API contract и principal

- [x] 1.1 Добавить в `api/openapi.yaml` session, post context, admin attachment, bans, audit и post-ban операции с безопасными DTO; перегенерировать код и проверить `go test ./api/contract ./api/generated`.
- [x] 1.2 Заменить request-ID actor map на типизированный authenticated principal в request context, сохранив аудит входов и серверные role checks; проверить unit-тестами параллельных запросов и ролей reports/moderator/administrator.
- [x] 1.3 Зарегистрировать новые маршруты только в management router и добавить contract/router-тесты, подтверждающие `404` для каждого маршрута на public listener.

## 2. Серверные административные чтения

- [x] 2.1 Реализовать session и cursor-paginated post context, включая hidden body и allowlist attachment metadata без IP/delete token; проверить integration-тестами visible, hidden, missing и role-denied случаи.
- [x] 2.2 Реализовать защищенную выдачу admin attachment с существующими MIME/size/hash проверками и проверить недоступность через public router и отказ для вложения вне разрешенного контекста.
- [x] 2.3 Реализовать paginated bans с active/history фильтром и безопасным fingerprint субъекта; проверить SQL/integration-тестами active, expired и revoked записи без утечки raw IP/HMAC.
- [x] 2.4 Реализовать paginated audit allowlist без payload и секретов; проверить ordering, pagination, failed action и отсутствие запрещенных полей contract-тестами.
- [x] 2.5 Реализовать создание lookup-блокировки по `post_id` без возврата IP/HMAC и проверить role checks, expiry, operations, аудит и фактическое применение блокировки integration-тестами.

## 3. Общая terminal-библиотека

- [x] 3.1 Вынести themes/MC skins, sanitization, text width и общую chrome-компоновку в `internal/terminalui`, перевести публичный TUI без изменения snapshots и проверить `go test ./internal/tui ./internal/terminalui`.
- [x] 3.2 Вынести command hit-testing и безопасные mouse helpers, сохранив приоритет верхней/нижней панелей над контентом; проверить одинаковыми keyboard/mouse contract-тестами публичный и административный chrome.
- [x] 3.3 Вынести reusable image viewer/cache boundary и гарантировать удаление `BORD_ADMIN_CREDENTIAL` из environment external opener; проверить viewer unit-тестами и существующим terminal smoke.

## 4. Admin HTTP client и credentials

- [x] 4.1 Реализовать `internal/adminclient` поверх generated client с bearer request editor, pagination и безопасным преобразованием ошибок; проверить каждую операцию через `httptest.Server`, включая 401/403/404/429/5xx.
- [x] 4.2 Реализовать загрузку credential по установленному приоритету без secret flag и с проверкой типа/прав файла; проверить POSIX permission, symlink, whitespace, environment precedence и redaction unit-тестами.
- [x] 4.3 Реализовать отдельное admin-состояние без credential и атомарное сохранение management URL/theme/view; проверить права создаваемого файла и отсутствие секрета в serialized state.

## 5. Admin TUI: чтение и навигация

- [x] 5.1 Создать `internal/admintui.Model` и экраны Жалобы/Блокировки/Аудит/Справка с async request IDs, cancellation и retry-safe refresh; проверить reducer-тестами stale responses, loading, errors и смену экранов.
- [x] 5.2 Реализовать paginated очередь жалоб и двухпанельный контекст поста/треда с wide/narrow layout, scroll и дедупликацией страниц; проверить golden-тестами размеров и keyboard/mouse навигации.
- [x] 5.3 Реализовать просмотр admin attachment через общий viewer и восстановление mouse mode после external/Kitty viewer; проверить fake-viewer тестами и terminal smoke fixture.
- [x] 5.4 Реализовать списки блокировок и аудита с active/history, пагинацией и безопасным отображением значений; проверить snapshots, курсоры и отсутствие чувствительных полей.

## 6. Admin TUI: действия модерации

- [x] 6.1 Реализовать общую reason/confirmation modal для hide, restore и close с блокировкой повторной отправки и server refresh после результата; проверить invalid reason, cancel, success и role-denied reducer-тестами.
- [x] 6.2 Реализовать создание ban из выбранного поста, выбор operations/TTL и отзыв активного ban; проверить формы, кликабельные hit zones, серверные ошибки и обновление списка тестами.
- [x] 6.3 Реализовать отдельную reveal-IP modal только для administrator с очисткой значения при закрытии/смене экрана и без auto-copy; проверить недоступность для младших ролей и отсутствие IP в последующем `View()`.
- [x] 6.4 Добавить help, theme dropdown, MC skins и согласованные горячие клавиши/мышь; проверить top/bottom buttons, narrow panel switch и theme persistence regression-тестами.

## 7. Бинарник, документация и выпуск

- [x] 7.1 Добавить `cmd/bord-admin` с `--management`, `--credential-file`, `--theme`, `--inline`, `--timeout`, `--version` и безопасной диагностикой; проверить command-тестами flags, defaults, compatibility error и отсутствие credential в output.
- [x] 7.2 Добавить `docs/admin-tui.md` и README-инструкции bootstrap, credential permissions, SSH tunnel/Tailscale, role matrix, IP scrollback risk и управление; проверить документированные команды в локальном smoke-сценарии.
- [x] 7.3 Расширить release/validation scripts admin-only матрицей для Linux/macOS/Windows amd64/arm64 и составом архивов; проверить reproducible build, archive validation и `sha256sum --check`.
- [x] 7.4 Провести end-to-end smoke на отдельном management listener: reports → hidden context → hide/restore → post ban/revoke → audit → administrator reveal, затем выполнить `make quality` и terminal smoke без регрессии публичного `bord`.

## Workflow follow-up

- После review и успешных security/release gates синхронизировать capability в основные specs и архивировать изменение.
- Развернуть совместимый backend раньше публикации `bord-admin` архивов.
