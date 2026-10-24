# Tasks

## 1. Terminal capabilities и session lifecycle

- [x] 1.1 Добавить инъецируемые capability detector и transport для Kitty placeholders и Sixel с ограниченным таймаутом, консервативным ANSI fallback и тестами успешных, неполных, multiplexed и timeout-ответов; проверить `go test ./internal/terminalui`.
- [x] 1.2 Добавить общую terminal image session с generation, уникальными внутренними ID и идемпотентной очисткой при замене, resize и `Close`; покрыть последовательность create/replace/delete/close unit-тестами.
- [x] 1.3 Подключить detector и гарантированный `Close` вокруг `tea.Program.Run` в `bord` и интегрированном `bord-admin`, исключив попадание probe-ответов в пользовательский ввод; проверить тестами entrypoint для успеха, ошибки и отмены context.

## 2. Kitty placeholders внутри кадра TUI

- [x] 2.1 Реализовать передачу PNG, виртуальное placement и строки `U+10EEEE` с кодированием image ID/row/column и ограничением прямоугольника; проверить golden-тестами APC payload, текстовую ширину строк и delete-команды.
- [x] 2.2 Добавить fuzz/property-тесты Kitty renderer на размеры области, image IDs и проверенные PNG, подтвердив отсутствие выхода за заданную ширину и интерполяции недоверенных control bytes.
- [x] 2.3 Встроить Kitty/ANSI frame renderer в пользовательский экран вложения, убрать автоматический `tea.Exec` переход, показать фактический backend и сохранить `Esc`/`Enter`/мышиную навигацию; проверить model-тестами открытие, возврат, resize, ошибку backend и внешний opener.

## 3. Sixel и выбор режима

- [x] 3.1 Подключить закрепленную pure-Go версию `github.com/mattn/go-sixel`, проверить лицензию, `govulncheck` и сборку без CGO для Linux/macOS/Windows; зафиксировать результаты в комментарии задачи или документации зависимости.
  - Закреплено `v0.0.14` (MIT). `govulncheck` под Go 1.27.3: вызываемых уязвимостей нет. `CGO_ENABLED=0` сборки `bord` и `bord-admin` для `linux/amd64`, `darwin/amd64`, `windows/amd64` прошли.
- [x] 3.2 Реализовать ограниченный Sixel frame renderer с масштабированием, резервированием строк и кэшем по вложению/размеру; покрыть unit- и fuzz-тестами DCS framing, пределы ресурсов и повторную генерацию только при изменении поколения.
- [x] 3.3 Расширить `InlineMode`, CLI и state значением `sixel`, реализовать порядок `auto: Kitty → Sixel → ANSI` и безопасный ANSI fallback для неподдерживаемого принудительного режима; проверить round-trip настроек и таблицу выбора renderer unit-тестами.
- [x] 3.4 Обновить окно настроек и экран вложения: отдельные Auto/Kitty/Sixel/ANSI/w3m/System варианты, фактический backend и понятное предупреждение о fallback; проверить клавиатурные и mouse model-тесты.

## 4. Административный TUI и единое поведение

- [x] 4.1 Перевести `admintui.TerminalImageViewer` на общий встроенный frame renderer и разделяемую terminal session без автоматического `tea.Exec`; проверить показ, навигацию, cleanup и ошибки fake renderer-тестами.
- [x] 4.2 Синхронизировать режим и capabilities между пользовательской и административной частью `bord-admin`, исключив оставшиеся placements при переключении режима; проверить integration-тестами `internal/adminapp`.

## 5. Документация и интеграционная проверка

- [x] 5.1 Обновить `README.md`, `docs/tui.md`, CLI help и лендинг: изображения показываются прямо в TUI, перечислить Kitty/Sixel/ANSI compatibility и оставить внешнее открытие явным fallback; проверить `scripts/validate-landing.py` и примеры команд.
- [x] 5.2 Добавить terminal smoke-сценарии, подтверждающие отсутствие отдельного viewer и корректную ширину кадра на Linux/macOS/Windows; выполнить `make quality` и `make terminal-smoke`.
- [ ] 5.3 Провести ручную матрицу на Kitty, актуальном iTerm2/WezTerm и Windows Terminal: открыть, изменить размер, сменить режим, вернуться и завершить приложение; сохранить обнаруженные ограничения в `docs/tui.md` до выпуска.

## Workflow follow-up

- После реализации собрать новую клиентскую версию, проверить release-архивы и только затем обновить загрузки и инструкции на лендинге.
- После приемки синхронизировать capability в основные specs и архивировать изменение.
