# Установка и запуск Bord TUI

`bord` — полноэкранный терминальный клиент для анонимной борды Bord. Он работает
на Linux, macOS и в современном Windows Terminal, поддерживает клавиатуру, мышь,
цветовые темы, ответы на посты и JPEG/PNG-вложения.

TUI не содержит backend: перед запуском нужен доступный `bord-server`, например
локальный `http://127.0.0.1:8080` или публичный `https://board.example.org`.

## Требования

- терминал с UTF-8 и ANSI-цветами;
- Linux, macOS или Windows Terminal;
- шрифт с символами псевдографики;
- сетевой доступ к HTTP(S)-адресу Bord backend;
- Go 1.27 — только если клиент собирается из исходников.

Старый Windows `cmd.exe` не поддерживается. PowerShell можно использовать как
shell внутри Windows Terminal.

## Просмотр изображений в терминале

Bord поддерживает три способа показа JPEG/PNG-вложений:

- **Нативный** — Bord преобразует изображение в PNG и передаёт его напрямую
  терминалу через Kitty graphics protocol. Этот режим автоматически
  распознаёт Kitty, актуальный iTerm2 и WezTerm.
- **ANSI** — изображение рисуется прямо в TUI символами `▀` и true-color
  цветами. Графический протокол не нужен, поэтому это вариант для Windows
  Terminal и других обычных ANSI-терминалов.
- **w3m** — Bord без shell запускает отдельно установленную команду `w3m`.

В TUI режим выбирается через `S` / `F6` → **Изображения**. При нативном показе
Bord временно освобождает полноэкранный интерфейс, выводит изображение на весь
доступный экран и возвращается по `Enter`.

### iTerm2

Актуальный iTerm2 реализует Kitty graphics protocol, поэтому отдельный Kitty
не требуется. Обновите iTerm2, запустите Bord внутри него и оставьте режим
**Нативный**:

```sh
brew upgrade --cask iterm2
bord
```

Со старой сборкой Bord, которая ещё не распознаёт `TERM_PROGRAM=iTerm.app`,
используйте `bord --inline kitty`. Если показ не работает внутри tmux, сначала
проверьте запуск напрямую в iTerm2; как универсальный fallback выберите ANSI.

### PowerShell в Windows

PowerShell — это shell: поддержку графики определяет терминал, в котором он
запущен. Windows Terminal пока не поддерживает используемый Bord Kitty graphics
protocol, поэтому в нём выберите **ANSI**.

Для нативного показа установите WezTerm и откройте в нём PowerShell 7:

```powershell
winget install --id wez.wezterm -e
wezterm start -- pwsh.exe -NoLogo
```

В новом окне WezTerm запустите `bord.exe`; режим **Нативный** будет выбран
автоматически. Для старой сборки Bord используйте `bord.exe --inline kitty`.

## Вариант 1: установка готового бинарника

Выберите файл под свою ОС и архитектуру:

| Платформа | Типичное имя файла |
|---|---|
| Linux x86-64 | `bord-linux-amd64` |
| Linux ARM64 | `bord-linux-arm64` |
| macOS Intel | `bord-darwin-amd64` |
| macOS Apple Silicon | `bord-darwin-arm64` |
| Windows x86-64 | `bord-windows-amd64.exe` |
| Windows ARM64 | `bord-windows-arm64.exe` |

Репозиторий умеет сформировать отдельную папку с шестью готовыми TUI-архивами:

```sh
python3 scripts/release.py --version v0.1.0 --output dist-tui --tui-only
python3 scripts/validate-release.py \
  --directory dist-tui --version v0.1.0 --tui-only
```

В `dist-tui/` появятся по одному архиву для каждой пары ОС/архитектуры,
`SHA256SUMS` и краткий `README.txt`. Полный этот гайд вложен в каждый архив.

Если рядом с release опубликованы checksums, проверьте SHA-256 до установки.

### Linux

```sh
chmod +x ./bord-linux-amd64
sudo install -m 0755 ./bord-linux-amd64 /usr/local/bin/bord
bord --version
```

Для установки только текущему пользователю:

```sh
install -d "$HOME/.local/bin"
install -m 0755 ./bord-linux-amd64 "$HOME/.local/bin/bord"
export PATH="$HOME/.local/bin:$PATH"
bord --version
```

Добавьте `$HOME/.local/bin` в `PATH` вашего shell profile, если каталога там ещё
нет.

### macOS

Для Apple Silicon:

```sh
chmod +x ./bord-darwin-arm64
sudo install -m 0755 ./bord-darwin-arm64 /usr/local/bin/bord
bord --version
```

Для Intel Mac замените имя файла на `bord-darwin-amd64`. Если macOS блокирует
загруженный файл, сначала проверьте источник, checksum и подпись release, затем
разрешите запуск в системных настройках безопасности. Не отключайте Gatekeeper
глобально.

### Windows Terminal

Откройте PowerShell в Windows Terminal:

```powershell
$installDir = "$env:LOCALAPPDATA\Programs\Bord"
New-Item -ItemType Directory -Force -Path $installDir | Out-Null
Copy-Item .\bord-windows-amd64.exe "$installDir\bord.exe"
```

Добавьте каталог в пользовательский `PATH`:

```powershell
$current = [Environment]::GetEnvironmentVariable("Path", "User")
if (($current -split ';') -notcontains $installDir) {
    [Environment]::SetEnvironmentVariable(
        "Path",
        (($current.TrimEnd(';') + ";" + $installDir).TrimStart(';')),
        "User"
    )
}
```

Закройте и заново откройте Windows Terminal, затем проверьте:

```powershell
bord.exe --version
```

## Вариант 2: сборка из исходников

```sh
git clone <URL-репозитория> bord
cd bord
mkdir -p dist
CGO_ENABLED=0 go build -trimpath -o dist/bord ./cmd/bord
./dist/bord --version
```

Установить собранный Linux/macOS-бинарник в `PATH`:

```sh
sudo install -m 0755 dist/bord /usr/local/bin/bord
```

Либо установить через Go из уже клонированного репозитория:

```sh
go install ./cmd/bord
```

В этом случае бинарник появится в `$GOBIN`, а если он не задан — в каталоге,
который возвращает `go env GOPATH`, с суффиксом `/bin`.

### Кросс-компиляция

```sh
GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o dist/bord-linux-amd64 ./cmd/bord
GOOS=linux   GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -o dist/bord-linux-arm64 ./cmd/bord
GOOS=darwin  GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -o dist/bord-darwin-arm64 ./cmd/bord
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o dist/bord-windows-amd64.exe ./cmd/bord
```

## Первый запуск

### Подключение к локальному backend

Если `bord-server` слушает `127.0.0.1:8080`:

```sh
bord --server http://127.0.0.1:8080
```

Для запуска прямо из репозитория без установки:

```sh
go run ./cmd/bord --server http://127.0.0.1:8080
```

Для текущего изолированного demo-каталога проекта команда выглядит так:

```sh
/tmp/opencode/bord-retro-demo-20261018/bin/bord \
  --server http://127.0.0.1:8080 \
  --config /tmp/opencode/bord-retro-demo-20261018/client/state.json \
  --theme classic \
  --inline off
```

Эта команда сработает, только если demo-бинарник и backend в указанном временном
каталоге ещё существуют.

### Подключение к публичному backend

```sh
bord --server https://board.example.org
```

Или через переменную окружения:

```sh
export BORD_SERVER=https://board.example.org
bord
```

PowerShell:

```powershell
$env:BORD_SERVER = "https://board.example.org"
bord.exe
```

Адрес выбирается в таком порядке:

1. `--server`;
2. `BORD_SERVER`;
3. адрес из локального state-файла;
4. `http://localhost:8080`.

Для публичного сервера используйте HTTPS. Клиент не предоставляет флаг для
отключения проверки TLS-сертификата.

## Параметры запуска

```text
bord [параметры]

--server URL
    Адрес Bord backend.

--config PATH
    Явный путь к локальному state-файлу.

--theme NAME|PATH.ini
    Встроенная тема, имя MC skin или путь к skin-файлу Midnight Commander.

--inline auto|kitty|w3m|ansi|off
    Режим показа изображений в терминале.

--timeout DURATION
    Таймаут HTTP-запросов, по умолчанию 15s.

--version
    Показать версию и завершить работу.
```

Примеры:

```sh
bord --server https://board.example.org --theme green
bord --server https://board.example.org --theme catppuccin-mocha
bord --server https://board.example.org --theme "$HOME/.local/share/mc/skins/custom.ini"
bord --server http://127.0.0.1:8080 --inline off --timeout 30s
bord --config "$HOME/.config/bord/test-state.json" --theme mono
```

## Локальное состояние и секреты

При первом запуске клиент создаёт state-файл. Стандартное расположение зависит
от ОС:

- Linux: `${XDG_CONFIG_HOME:-$HOME/.config}/bord/state.json`;
- macOS: `~/Library/Application Support/bord/state.json`;
- Windows: `%AppData%\bord\state.json`.

Точный путь можно переопределить через `--config`.

State содержит:

- последний открытый раздел;
- выбранную тему;
- анонимный session secret;
- delete secrets созданных публикаций;
- незавершённые delete secrets для безопасного повтора запроса.

На Unix файл и каталог создаются с закрытыми правами, а запись выполняется
атомарно. Никогда не публикуйте state-файл и не отправляйте его в bug report.
Утрата state означает утрату локальных прав на удаление своих публикаций;
получивший копию файла может воспользоваться этими правами.

Для разных серверов рекомендуется использовать разные state-файлы:

```sh
bord --server https://board-one.example --config "$HOME/.config/bord/board-one.json"
bord --server https://board-two.example --config "$HOME/.config/bord/board-two.json"
```

## Управление

### Каталог досок и тредов

| Клавиша | Действие |
|---|---|
| `↑` / `k`, `↓` / `j` | перемещение выбора |
| `Page Up` / `Page Down` | переход на страницу тредов |
| `Tab` | сменить активную панель |
| `Enter` | открыть выбранную доску, тред или полный текст поста |
| `n` / `F7` | создать новый тред |
| `r` / `F5` | обновить данные |
| `/` / `F4` | поиск |
| `Esc` | вернуться назад |

### Открытый тред

| Клавиша | Действие |
|---|---|
| `Enter` | открыть полный текст выделенного поста |
| `Page Up` / `Page Down` | переход на страницу постов |
| `p` / `F7` | ответить в тред без ссылки на пост |
| `P` / `F8` | ответить на выделенный пост |
| `o` | загрузить и проверить вложение выбранного/открытого поста |
| `Esc` | вернуться в каталог |

В режиме полного текста используйте `↑`/`↓` или колесо мыши для прокрутки,
`F8` для ответа на открытый пост и `Esc` для возврата в тред.

Новый тред можно создать только из каталога. Внутри треда нижняя панель явно
показывает `Reply Thread` и `Reply Post`. В редакторе перед отправкой отображается
цель: доска, тред или конкретный `>>post_id`.

### Редактор публикации

| Клавиша | Действие |
|---|---|
| `Ctrl+S` | отправить или повторить после ошибки |
| `Ctrl+O` | выбрать JPEG/PNG-вложение |
| `Tab` | переключить панели ответа и исходного поста |
| `Shift` + стрелки | выделить текст исходного поста |
| `Ctrl+C` | скопировать выделенный текст исходного поста |
| `Esc` | вернуться, сохранив черновик |
| `Ctrl+C` | завершить клиент, когда редактор ответа активен |

Во время активного запроса повторный `Ctrl+S` игнорируется. После сетевой ошибки
черновик сохраняется, а повтор использует тот же idempotency key и не должен
создавать дубликат.

### Общие команды и мышь

| Клавиша | Действие |
|---|---|
| `t` / `F2` | открыть выпадающий список тем |
| `?` / `F1` | открыть справку |
| `q`, `Ctrl+C`, `F10` | выйти |

Мышь поддерживает:

- открытие доски одним щелчком без предварительного отдельного выделения;
- открытие треда или полного поста одним щелчком;
- переход к исходному посту щелчком по `>>post_id` в строке ответа;
- ответ на выбранный пост через `F8 Reply Post`;
- переключение панелей ответа и исходного поста щелчком;
- drag-выделение текста исходного поста с последующим `Ctrl+C`;
- прокрутку панели под указателем колёсиком ровно на одну запись;
- верхнее меню, нижнюю панель команд и кнопки редактора.

В списке досок число в квадратных скобках означает количество тредов, а в
каталоге рядом с ID треда — количество ответов без стартового поста.

## Темы

Встроены четыре темы:

- `classic` — классическая синяя тема Midnight Commander;
- `amber` — янтарная;
- `green` — зелёный phosphor;
- `mono` — монохромная.

Дополнительно Bord импортирует совместимые `.ini` skins Midnight Commander из
`$XDG_DATA_HOME/mc/skins`, `$XDG_CONFIG_HOME/mc/skins`,
`~/.local/share/mc/skins`, `~/.config/mc/skins`, `/usr/local/share/mc/skins` и
`/usr/share/mc/skins`. Поддерживаются aliases, truecolor/именованные цвета и
символы двойных рамок из секций `[aliases]`, `[core]`, `[menu]` и `[Lines]`.

`--theme` принимает встроенное имя, имя MC skin без `.ini` или прямой путь к
файлу. `t`/`F2` либо щелчок по полю `Тема` открывает выпадающий список;
`↑`/`↓`, `Page Up`/`Page Down` и `Enter` выбирают тему. Итоговый выбор
сохраняется в state-файле.

## Изображения

Режим задаётся через `--inline`:

- `auto` — автоматически использовать поддерживаемую inline-графику;
- `kitty` — принудительно использовать Kitty graphics protocol;
- `w3m` — открыть проверенный файл во внешнем терминальном браузере `w3m`;
- `ansi` — показать изображение внутри Bord цветными ANSI half-block ячейками;
- `off` — не рисовать изображение внутри терминала.

В верхней панели пункт `Настройки` (`s`/`F6`) открывает выпадающее меню как у
тем. Пункт `Изображения` показывает центральное окно выбора между нативным
режимом, `w3m` и ANSI. Выбор сохраняется в локальном state-файле; по умолчанию
используется прежний нативный режим `auto`.

В открытом треде выделите пост с вложением или откройте его полный текст, затем
нажмите `o`. Клиент скачает файл, проверит Content-Type, размер и SHA-256. В
Kitty, iTerm2 или WezTerm Bord временно приостановит текстовый экран и покажет
изображение прямым terminal-viewer; `Enter` вернёт к тому же посту. `--inline
kitty` также позволяет принудительно включить протокол в другом совместимом
терминале.

ANSI-режим не использует графические протоколы терминала и остаётся внутри
Bubble Tea. `w3m` запускается без shell и требует установленную команду `w3m`;
если она отсутствует, Bord показывает ошибку и сохраняет возможность открыть
тот же проверенный файл системным приложением.

Нажмите `Enter` на экране изображения, чтобы открыть проверенный файл системным
приложением. Это основной режим для Windows Terminal, стандартного Terminal.app
и других терминалов без Kitty graphics protocol. Opener запускается напрямую,
без shell, а файл сохраняется в пользовательском cache с правами `0600`.

Если терминал искажает экран или не поддерживает graphics protocol:

```sh
bord --server https://board.example.org --inline off
```

## Обновление

1. Завершите клиент через `q` или `F10`.
2. Не удаляйте state-файл.
3. Проверьте checksum нового бинарника.
4. Замените только executable.
5. Запустите `bord --version`, затем обычную команду подключения.

Linux/macOS:

```sh
sudo install -m 0755 ./bord-new /usr/local/bin/bord
bord --version
```

Windows PowerShell:

```powershell
Copy-Item -Force .\bord-new.exe "$env:LOCALAPPDATA\Programs\Bord\bord.exe"
bord.exe --version
```

Формат state-файла проверяется клиентом. Повреждённый state не читается как
секрет: клиент перемещает его в файл с суффиксом `.corrupt-*` и создаёт новое
состояние.

## Диагностика

### Не удаётся подключиться

Проверьте backend вне TUI:

```sh
curl --fail https://board.example.org/healthz
curl --fail https://board.example.org/readyz
curl --fail https://board.example.org/api/v1/boards
```

Для локального запуска замените адрес на `http://127.0.0.1:8080`. Проверьте
схему URL, DNS, firewall, TLS-сертификат и системное время.

### Не обновляются новые ответы

TUI использует SSE как сигнал для повторного чтения HTTP. Проверьте поток:

```sh
curl -N https://board.example.org/api/v1/events
```

Соединение должно оставаться открытым и получать heartbeat. Proxy/CDN не должен
буферизовать этот endpoint.

### Неверные рамки или цвета

- используйте современный UTF-8 терминал;
- выберите шрифт с символами `╔═║╗╚╝`;
- попробуйте `--theme mono`;
- проверьте, что `TERM` не установлен в `dumb`;
- на Windows запускайте клиент именно в Windows Terminal.

### Мышь не работает

Убедитесь, что терминал поддерживает mouse reporting. Некоторые multiplexer или
удалённые сессии требуют включить mouse mode отдельно. Все действия доступны с
клавиатуры, даже если терминал не передаёт mouse events.

### TUI сломан после аварийного завершения

Обычно Bubble Tea восстанавливает alternate screen автоматически. Если shell
остался в необычном режиме, выполните:

```sh
reset
```

## Удаление клиента

Linux/macOS при системной установке:

```sh
sudo rm /usr/local/bin/bord
```

Windows PowerShell:

```powershell
Remove-Item "$env:LOCALAPPDATA\Programs\Bord\bord.exe"
```

State-файл удаляйте только осознанно: вместе с ним будут потеряны локальные
delete secrets. Для полного удаления после проверки пути:

```sh
rm -rf "$HOME/.config/bord"                         # Linux
rm -rf "$HOME/Library/Application Support/bord"   # macOS
```

PowerShell:

```powershell
Remove-Item -Recurse -Force "$env:APPDATA\bord"
```

## Смежная документация

- [Главный README и развёртывание backend](../README.md)
- [Терминальный smoke matrix](terminal-smoke-matrix.md)
- [OpenAPI-контракт](../api/openapi.yaml)
