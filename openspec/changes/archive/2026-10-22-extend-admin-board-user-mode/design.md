# Design

## Context

См. `proposal.md` — Why. Сейчас `bord-admin` владеет отдельной Bubble Tea model для reports/bans/audit, вручную повторяет panel renderer и имеет упрощенный theme overlay. Обычный `internal/tui.Model` уже реализует полный public workflow, mouse hit-testing, theme picker и image viewer. Таблица `boards` уже содержит `enabled`, но public read paths не везде трактуют выключенную доску как отсутствующую, а management API не управляет досками.

Ограничения:

- public и management listeners остаются раздельными;
- management credential нельзя передавать public transport или сохранять в state;
- «удаление» доски означает архивирование, а не каскадное удаление;
- пользовательский режим должен оставаться поведенчески тем же клиентом, а не второй реализацией;
- Linux, macOS и современный Windows Terminal должны сохранять корректную геометрию и ввод.

## Goals / Non-Goals

### Goals

- Добавить administrator-only create/archive/restore/list-all board operations с аудитом.
- Закрыть все public read/write пути к архивной доске, сохранив management visibility.
- Встроить настоящий `internal/tui.Model` в `bord-admin` и переключать режимы без запуска дочернего процесса.
- Использовать общий panel/chrome и один theme picker в обоих TUI.
- Сохранить независимые public user state и admin state/credential boundaries.

### Non-Goals

- Физическое или каскадное удаление досок и контента.
- Переименование slug, перенос тредов между досками или массовая модерация.
- Наделение пользовательского режима административными возможностями.
- Изменение публичного бинарника `bord` или его сетевого контракта, кроме повторного использования вынесенных UI-компонентов.
- Автоматическое хранение raw IP или изменение выбранной production privacy policy.

## Decisions

### 1. Состояние `enabled` является источником истины для архива

Архивирование атомарно меняет существующий `boards.enabled` с `1` на `0`; восстановление — обратно. Новая schema migration не нужна. Repository получает отдельные запросы list-all, create и compare-current-state update, а public `ListBoards` продолжает возвращать только active records.

Все board mutations выполняются через integration admin adapter в транзакции с audit append. Slug уникален среди активных и архивных записей. Повтор операции над тем же состоянием — conflict, а не успешный no-op.

Почему: существующая модель уже выражает нужный lifecycle, а новая `archived_boards` или soft-delete timestamp создали бы лишнюю миграцию. Время и причина операции доступны из обязательного audit trail.

### 2. Архивная доска на public surface эквивалентна отсутствующей

Публичный application service проверяет active board не только для catalog/create-thread, но и по parent chain для posts, replies, delete и attachment metadata/download. Прямые ID не обходят ограничение. Public handlers нормализуют результат в not-found, чтобы не раскрывать существование архива. Management context использует отдельные repository methods и продолжает видеть контент.

При архивировании outbox не удаляется. Новые public события для этой доски не создаются, а текущие подписчики при следующем refresh получают not-found/исчезновение доски. Restore возвращает чтение и запись существующего контента.

### 3. Board API отделен от public API и доступен только administrator

OpenAPI получает management routes:

- `GET /admin/v1/boards` с cursor, limit и state filter;
- `POST /admin/v1/boards` с `slug`, `title`, `reason`;
- `POST /admin/v1/boards/{board_id}/archive` с `reason`;
- `POST /admin/v1/boards/{board_id}/restore` с `reason`.

Ответ списка содержит только безопасные board metadata и агрегированные counts. Middleware проверяет principal role до вызова adapter. Request ID используется только для корреляции/audit. Public router эти handlers не регистрирует.

### 4. Верхнеуровневая app model владеет двумя существующими моделями

`bord-admin` получает root app model с `admin` child model, `user` child `internal/tui.Model`, active mode и shared theme catalog. Root model:

- перехватывает mode-menu keyboard/mouse events до дочерней модели;
- передает `WindowSizeMsg` с доступной геометрией активному child;
- сохраняет обе child models при переключении, поэтому selection, pagination и draft не теряются;
- делегирует остальные messages только модели-владельцу асинхронной команды;
- объединяет shutdown/cancel обеих моделей.

Обычная user model не переписывается. Она получает небольшой host/navigation hook для embedded top bar; standalone `bord` использует default hook и сохраняет прежний вид и поведение.

Почему не subprocess: отдельный `bord` потерял бы единое управление мышью/темой, усложнил terminal restore и не удовлетворил бы переключение внутри одного TUI.

### 5. Public bootstrap переиспользует обычный client stack

Композиция public API, posting API, SSE, cache, opener и user state выносится из `cmd/bord` в reusable constructor без CLI-зависимостей. `cmd/bord` и `cmd/bord-admin` вызывают один constructor. Для admin binary `--server`/`BORD_SERVER` настраивает только public client; `--management` настраивает только admin client.

Используются два отдельных `http.Client`/transport wrapper. Только admin client знает credential. Тестовый recording transport доказывает отсутствие `Authorization` во всех public HTTP/SSE requests.

### 6. User state и admin state остаются физически и логически раздельными

User child использует существующий обычный client store для anonymous session, delete capabilities и последней доски. Admin store сохраняет active mode, admin screen, theme setting и несекретные UI preferences. Management credential по-прежнему загружается только из environment/защищенного файла и не попадает ни в один JSON.

Theme является общей presentation preference: root model применяет выбранный `ThemeName` к обоим children, но сохраняет setting только через admin state в рамках запуска `bord-admin`. Обычный user secrets store не копируется в admin state.

### 7. Panel renderer и theme picker становятся shared terminalui components

Из `internal/tui` извлекаются:

- типизированные display rows и panel renderer с title/hint, active selection и trusted-ANSI flag;
- split-panel joining/geometry helpers;
- stateful framed theme picker: geometry, viewport/scroll, keyboard update, mouse hit-testing и overlay rendering.

`internal/tui` сначала переводится на shared компоненты с golden/regression tests без видимых изменений. Затем `internal/admintui` удаляет локальные `panel` и `renderThemeMenu`, использует те же компоненты и line set. Это гарантирует одинаковые углы, hint row, selection colors, narrow fallback и clipping, а не приблизительное копирование.

Theme picker повторяет текущую семантику обычного клиента: открывается у top control, фокусируется на текущей теме, применяет выбор только по Enter/клику и отменяется без изменения по Esc/F2/t/клику снаружи. Длинный каталог имеет viewport и scrolling.

### 8. Экран «Доски» следует destructive-action policy

В admin navigation добавляется fourth screen `Доски`. Create modal собирает slug, title и reason. Archive/restore modal показывает target и требует reason длиной 3–500 символов плюс повторный Ctrl+S/Enter. В UI используется термин «Архивировать», а не «Удалить», чтобы не обещать физическое удаление.

Только administrator видит actionable create/archive/restore controls. Остальные роли не получают UI affordance, но серверная проверка остается обязательной.

## Risks / Trade-offs

- **Проверка parent board расширяет public hot paths.** Добавить targeted repository joins/queries и измерить существующий bounded profile; не выполнять N+1 на списках.
- **Две Bubble Tea модели могут спутать async messages.** Root использует distinct wrapper message types или owner tags и не передает чужие result messages неактивной модели.
- **Shared UI extraction может изменить обычный Bord.** Сначала зафиксировать текущие golden/mouse tests, затем выполнять mechanical extraction и terminal smoke на всех target builds.
- **Архивирование при активных SSE clients не мгновенно очищает экран.** Сервер запрещает последующие reads/writes; клиентский refresh нормализует состояние. Отдельный realtime board-archived protocol сейчас не вводится.
- **Restore снова публикует старый контент.** Это намеренное обратимое поведение; причина и actor остаются в audit.

## Migration Plan

1. Расширить repository/application checks и management contract, не включая UI controls.
2. Прогнать migration/status на копии production DB; schema version не меняется.
3. Извлечь shared panel/theme picker и доказать отсутствие regression обычного `bord` golden tests.
4. Добавить root app model, public bootstrap и board screen.
5. Перед production deploy создать проверяемый backup, развернуть backend первым и выполнить public/admin compatibility smoke.
6. Обновить локальный `bord-admin`; проверить create → public visibility → archive → public 404/write rejection → management visibility → restore.

Rollback бинарника безопасен, поскольку schema не меняется. Доски, архивированные новой версией, останутся `enabled=0`; старый public list также их скрывает, но перед rollback оператор должен восстановить необходимые доски новой версией.

## Open Questions

Нет. Выбраны архивирование вместо физического удаления, встроенный полный user mode и рамки/theme picker обычного Bord.
