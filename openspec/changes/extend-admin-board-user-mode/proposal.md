# Proposal

## Why

После установки `bord-admin` оператор по-прежнему не может управлять жизненным циклом досок и вынужден покидать административный интерфейс для проверки пользовательского опыта. Одновременно рамки административных панелей визуально расходятся с обычным Bord, из-за чего общий стиль выглядит незавершенным.

## What Changes

- В management API добавляются просмотр всех досок, создание доски, безопасное архивирование вместо физического удаления контента и восстановление архивной доски.
- Архивированная доска исчезает из публичного каталога, запрещает новые треды и сохраняет существующие треды, посты, вложения и аудит.
- В `bord-admin` добавляется экран управления досками с обязательной причиной и двухшаговым подтверждением архивирования/восстановления.
- В верхнее меню `bord-admin` добавляется режим «Пользователь», использующий те же публичные API, state, публикацию, темы, мышь и просмотр вложений, что обычный бинарник `bord`.
- Административные панели, разделители, углы, narrow layout и modal-рамки приводятся к тем же правилам компоновки, что у обычного Bord, без отдельной копии renderer-кода.
- Выбор темы в `bord-admin` заменяется тем же рамочным popup/modal-компонентом, что в обычном Bord, включая MC skins, прокрутку, клавиатуру, мышь, подтверждение и отмену.
- `bord-admin` получает отдельную настройку public API URL; management credential никогда не отправляется на public listener.

## Capabilities

### New Capabilities

- `admin-board-lifecycle`: безопасное создание, перечисление и архивирование досок через management API с аудитом и серверной проверкой роли.
- `integrated-admin-user-mode`: переключение между административным и полноценным пользовательским режимами, единый theme picker и общий chrome в одном terminal-клиенте при строгом разделении public и management transport.

### Modified Capabilities

Нет: основные capability specs еще не синхронизированы из предыдущего change в `openspec/specs/`.

## Impact

- `api/openapi.yaml`, generated client и management-only router.
- SQLite board repository, integration admin adapter и audit actions без удаления существующих данных.
- `internal/adminclient`, `internal/admintui`, `internal/tui` и общий `internal/terminalui` chrome/layout boundary.
- `cmd/bord-admin`, локальное безопасное состояние и новая настройка public URL.
- Документация, terminal smoke, release regression tests и production compatibility checks.
