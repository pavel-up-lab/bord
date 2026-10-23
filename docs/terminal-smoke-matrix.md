# Матрица smoke-проверок терминального клиента

Матрица запускается одной командой из корня репозитория:

```sh
make terminal-smoke
```

Скрипт `scripts/terminal-smoke.sh` выполняет все тесты `internal/client`,
`internal/tui` и `cmd/bord` на текущей ОС, запускает собранный host-бинарник и
кросс-компилирует клиент вместе с test binaries для `linux/amd64`,
`darwin/amd64` и `windows/amd64`. Workflow `terminal-smoke.yml` дополнительно
запускает этот же скрипт на нативных GitHub runners всех трех ОС.

## Покрытие

| Возможность | Linux | macOS | Windows Terminal | Автоматизированное доказательство |
|---|---|---|---|---|
| Сборка `bord` | cross-build + native run | cross-build + native CI | cross-build + native CI | `scripts/terminal-smoke.sh` |
| Узкий режим (<72 колонок) | model fixture | model fixture | model fixture | `TestLayoutGolden/narrow` |
| Полный keyboard flow и mock API | model/subprocess | model/subprocess | model/subprocess | `TestKeyboardOnlySmokeAndGracefulQuit`, `TestScriptedFlowAgainstMockServer` |
| External opener | `xdg-open` | `open` | `rundll32.exe` file association | platform-specific `TestTerminalSmokeOpenerCommand`; argv передается без shell |
| Kitty auto/override/off и fallback | model fixture | model fixture | model fixture | `TestKittyRendererAndFallback` и golden escape sequence |
| ANSI half-block viewer | model fixture | model fixture | model fixture | `TestRenderANSIImageUsesBoundedHalfBlocks`, `TestANSIImageModeRendersInsideAttachmentScreen` |
| w3m viewer | native command, если установлен | native command, если установлен | ожидаемый fallback | argv передаётся напрямую без shell; отсутствие команды обрабатывается как ошибка viewer |
| Ошибка верхнего уровня и restoration | subprocess | subprocess | subprocess | `TestTerminalRestorationSubprocess` проверяет выход из alternate screen |
| SIGTERM и restoration | native subprocess | native subprocess | неприменимо | `TestTerminalSmokeSignalRestoresAlternateScreen`; Windows использует keyboard/error fixtures |

## Зафиксированное локальное выполнение

На Linux запуск `make terminal-smoke` проверяет нативный Linux-бинарник и тесты,
а также успешно создает macOS/Windows binaries и platform-specific test
binaries. Это **не** считается нативным UI-запуском macOS или Windows:
нативное исполнение этих fixtures выполняет CI matrix на соответствующих
runners. Обновлять эту формулировку на «проверено вручную» можно только после
реального запуска в целевом терминале.

## Поддержанные capabilities

- базовый ANSI TUI и alternate screen; однопанельный fallback для узкого окна;
- полное управление с клавиатуры и текстовая информация о вложении;
- Kitty graphics только при положительном capability detection либо явном
  `--inline=kitty`; `--inline=off` всегда запрещает inline rendering;
- `--inline=ansi` использует только true-color ANSI и Unicode half-block ячейки;
  `--inline=w3m` требует отдельно установленную команду `w3m`;
- внешний opener является гарантированным fallback и запускается напрямую,
  без интерпретации имени файла shell;
- штатный выход, `Ctrl+C`, ошибка ввода и SIGTERM на Unix завершают программу с
  восстановлением alternate screen.

## Известные ограничения

- кросс-компиляция не доказывает поведение конкретного эмулятора терминала,
  шрифтов, цветов, IME, clipboard или accessibility API;
- Windows fixture подтверждает Windows Terminal-совместимую модель и команду
  file association, но не вызывает GUI-приложение в CI;
- автоматическое обнаружение inline graphics ограничено Kitty; Sixel, iTerm2
  images и изображения Windows Terminal не поддерживаются;
- принудительное завершение процесса (`SIGKILL`, Task Manager End Process,
  авария ОС) не позволяет приложению восстановить терминал;
- ручная визуальная приемка на физических macOS/Windows Terminal остается
  release-проверкой и не подменяется Linux cross-build.
