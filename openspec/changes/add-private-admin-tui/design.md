# Design

## Context

См. `proposal.md` — Why. Сейчас сервер уже разделяет public и management listeners: `NewManagementRouter` пропускает только health/readiness и `/admin/v1/*`, а bearer credential проверяется отдельным middleware. Management API умеет получить жалобы и выполнить основные мутации, но не отдает роль текущего оператора, контекст скрытого поста, блокировки и аудит. Существующий `internal/tui` содержит пригодные визуальные решения, однако его модель напрямую связана с публичными boards/threads/posts и не должна становиться admin-режимом.

## Goals / Non-Goals

**Goals:**

- Отдельный `bord-admin` с узнаваемой компоновкой Bord и независимой административной state machine.
- Полный workflow «жалоба → контекст → подтвержденное действие → обновленный результат».
- Серверная авторизация каждой операции и отсутствие административных маршрутов на public listener.
- Повторное использование недоменных terminal-компонентов вместо копирования исправлений между двумя TUI.
- Безопасная работа с credential и IP при запуске на Linux, macOS и Windows Terminal.

**Non-Goals:**

- Встраивание admin-режима в обычный бинарник `bord`.
- Публикация management listener через `bord.pw` или реализация автоматического SSH-туннеля.
- Создание и ротация admin identities из TUI; bootstrap/revoke identities остаются операторскими командами сервера.
- Массовые действия, автоматическая модерация и изменение политики retention.
- Поддержка старого `cmd.exe`.

## Decisions

### 1. Отдельная модель, общая terminal-библиотека

Создается `internal/admintui` со своими экранами, сообщениями и интерфейсом сервиса. Из `internal/tui` в нейтральный `internal/terminalui` выносятся только общие элементы: темы и MC skin import, безопасная очистка текста, измерение ширины, верхняя/нижняя chrome-панель, command hit-testing и image viewer. Публичный TUI переводится на ту же библиотеку без изменения поведения.

Буквальное копирование текущего `Model` быстрее в начале, но приведет к двум версиям уже исправлявшихся mouse hit zones, theme discovery и terminal sanitation. Наследование admin-модели от публичной также отвергнуто: набор экранов и разрешенных переходов различается, а случайный доступ к posting API в admin-клиенте не нужен.

### 2. Три основных представления и модальные действия

Верхняя строка переключает `Жалобы`, `Блокировки`, `Аудит`, `Тема`, `Справка`. На широком экране жалобы показываются слева, контекст треда справа; на узком экране `Tab` переключает панели. `Enter` открывает полный пост или вложение.

Hide/restore, close, ban, revoke и reveal открывают модальную форму причины. Форма показывает точное действие и цель, не разрешает отправку причины вне серверных ограничений и требует повторного подтверждения. Во время запроса команда блокируется, а после ответа контекст перечитывается с сервера. Reveal использует отдельную предупреждающую модаль и очищает отображенное значение при уходе с экрана.

### 3. Расширение management API read-маршрутами

OpenAPI получает следующие management-only операции:

- `GET /admin/v1/session` — ID, role и срок действия текущей identity;
- `GET /admin/v1/posts/{post_id}/context` — board/thread и ограниченное окно постов, включая сохраненный текст скрытых постов и attachment metadata;
- `GET /admin/v1/attachments/{attachment_id}` — проверенное содержимое вложения из административного контекста;
- `GET /admin/v1/bans` — cursor pagination и фильтр active/history;
- `GET /admin/v1/audit` — cursor pagination безопасных audit DTO;
- `POST /admin/v1/posts/{post_id}/bans` — блокировка lookup-субъекта поста без передачи исходного IP или HMAC клиенту.

Существующие mutation routes сохраняются. DTO контекста формируется отдельным запросом и никогда не включает `request_subjects`, IP envelope, delete token или credential. Ban DTO показывает безопасное описание субъекта; lookup отображается коротким необратимым fingerprint. Audit DTO строится из явно разрешенных колонок и не отдает payload.

Альтернатива — собирать контекст через public API — отвергнута: по одному `post_id` нельзя надежно найти тред, а hidden body намеренно отсутствует в публичном ответе.

### 4. Явный principal вместо определения возможностей по ошибкам

Management authentication возвращает типизированный principal с identity ID, role и expiry и помещает его в request context. Сервисные методы принимают principal или извлекают его из контекста и продолжают проверять требуемую роль на сервере. `GET /session` позволяет TUI заранее скрыть недоступные команды, но не является границей безопасности.

Текущая request-ID map в integration adapter будет заменена контекстным principal, чтобы параллельные read-запросы TUI не зависели от одноразового извлечения actor. Request ID сохраняется отдельно для аудита. Все попытки аутентификации и мутации продолжают записываться без bearer.

### 5. Отдельный HTTP boundary для admin-клиента

`internal/adminclient` оборачивает generated OpenAPI client, добавляет `Authorization: Bearer` через request editor, нормализует domain errors и реализует интерфейс `admintui.Service`. Модель не работает с HTTP или generated response unions напрямую, что позволяет тестировать все переходы fake-сервисом.

Клиент использует один management base URL. Доступ к публичному API ему не требуется: весь необходимый контекст и attachments приходят через management listener.

### 6. Credential и локальное состояние разделены

Приоритет credential: `BORD_ADMIN_CREDENTIAL`, затем путь из `--credential-file`/`BORD_ADMIN_CREDENTIAL_FILE`, затем `~/.config/bord/admin.credential`. Значение credential не допускается в flags. На POSIX файл принимается только если это обычный файл текущего пользователя без group/other permissions; символические ссылки отклоняются. На Windows используются доступные проверки типа файла, а документация рекомендует ACL текущего пользователя.

`~/.config/bord/admin.json` сохраняет только management URL, тему и последний безопасный экран. Ошибки и terminal output проходят sanitization и никогда не включают request headers или credential.

### 7. Совместимость и выпуск

`bord-admin` поддерживает `--management`, `--credential-file`, `--theme`, `--inline`, `--timeout` и `--version`. Release tooling строит отдельные `bord-admin_<version>_<os>_<arch>` архивы с `docs/admin-tui.md`, LICENSE и THIRD_PARTY_NOTICES. Их публикация не означает открытый admin-доступ: сервер по-прежнему требует приватную сеть и credential.

Admin-клиент проверяет `/admin/v1/session` при запуске. Сервер без требуемых read-операций получает понятную ошибку несовместимой версии, а не частично работающий интерфейс.

## Risks / Trade-offs

- **[Рефакторинг общих TUI-компонентов может изменить пользовательский интерфейс]** → сохранить golden tests публичного TUI и добавить одинаковые mouse/keyboard contract tests для обеих моделей.
- **[Hidden content становится доступен через новый API]** → маршрут остается только на management listener, требует роль moderator+, использует allowlist DTO и покрывается тестом недоступности через public router.
- **[Credential из environment наследуется дочерними процессами]** → не передавать environment admin-клиента в external image opener; рекомендовать credential-файл как основной production-вариант.
- **[Показанный IP может остаться в scrollback]** → reveal выводится в перерисовываемой modal-панели, не копируется автоматически и очищается при закрытии; terminal scrollback полностью контролировать невозможно, что явно документируется.
- **[Списки изменяются во время cursor pagination]** → сортировать по монотонному ID и дедуплицировать элементы на клиенте; refresh начинает новую последовательность.
- **[Admin binary увеличивает release matrix]** → добавить отдельный admin-only режим release script и валидировать архивы тем же механизмом SHA-256.

## Migration Plan

1. Добавить и протестировать OpenAPI-контракты, principal context и read adapters без включения нового публичного маршрута.
2. Развернуть backend с расширенным management API; существующий публичный клиент продолжает работать без изменений.
3. Собрать и проверить `bord-admin` через локальный management listener и SSH-туннель, затем выпустить cross-platform архивы.
4. При rollback удалить admin binary и откатить backend; новые операции только читают существующие таблицы, поэтому отдельная миграция данных не требуется.
