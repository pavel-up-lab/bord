# MVP release checklist

Дата локальной проверки: 2026-10-17. Целевой commit release/staging:
`6b935f997a35`; baseline quality gate 8.4: `a00cb00`.

## Выполненные локальные gates

- [x] Go `go1.27.2 linux/amd64`, Python `3.14.4`.
- [x] Caddy `v2.11.7`: `caddy validate` и реальный local-TLS reverse proxy.
- [x] SSE heartbeat и reconnect с `Last-Event-ID` прошли через Caddy без buffering.
- [x] Upgrade, verified backup, разрушение staging-БД/blob-каталога, restore,
  readiness, логическое сравнение данных и rollback совместимого бинарника.
- [x] Четыре release-архива построены дважды с одинаковыми checksums; Linux
  клиент/сервер исполнены, macOS/Windows архивы прошли безопасную проверку.
- [x] `govulncheck v1.9.0`: вызываемых уязвимостей нет.
- [x] `go-licenses v1.6.0`: forbidden/restricted лицензий нет; ручное исключение
  `modernc.org/mathutil v1.7.1` закреплено точной версией и notice.
- [x] CycloneDX `cyclonedx-gomod v1.9.0` сформировал source SBOM.
- [x] Версия миграции: `3`; operator docs: `install.md`, `runbook.md`, `release.md`.
- [x] Beta defaults: loopback upstream, 16 MiB edge body limit, raw-IP retention
  90 дней, inline-графика клиента не включается без подтвержденной capability.

## Quality gate 8.4

- [x] `./scripts/quality-gate.sh` (`FUZZTIME=5s`, `REPEAT=5`) прошел на
  `go1.27.2 linux/amd64`: внутри выполнены `go test ./...`,
  `go test -race ./...`, повторные race-тесты HTTP strict decoding,
  SSE parser/reconnect/reset, hub, media normalization/resource bounds,
  client cancellation и server shutdown, затем `make contract` и
  `go vet ./...`.
- [x] Явные leak assertions `TestHTTPClientCancellationReleasesHandlers` и
  `TestSubscribeCancellationReleasesHandlers` по 20 циклов подтверждают ноль
  оставшихся HTTP/SSE handlers после cancel; весь выбранный набор дополнительно
  прошел под race detector пять раз.
- [x] Ограниченный fuzz: `FuzzParseSSEFragmentationIDsAndMultiline` — 382011
  executions за 6.0 s; `FuzzNormalize` — 408875 executions за 6.0 s. Оба
  запуска завершились `PASS`; лимиты цели: SSE event 1 MiB, media input 5 MiB,
  6000×6000 и 16 MP.
- [x] Gate выявил и закрыл два дефекта hardening: client success decoder теперь
  отклоняет unknown JSON fields, а signal smoke синхронизирует обработку
  `SIGTERM` до проверки восстановления alternate screen и стабильно проходит
  под `-race` (`-count=20`).

## Внешние и продуктовые blockers

- [ ] Admin bootstrap/revoke, ручной retention и re-encryption key rotation не
  подключены к production `bord-server`; task 7.5 остается открытой.
- [ ] OpenSpec task 8.8 принадлежит другой проверке и еще открыта; 8.4–8.6
  закрыты локальными quality/load/terminal gates.
- [ ] Hosted GitHub Actions, GitHub Release assets, публичный DNS/ACME и нативное
  исполнение macOS/Windows нельзя честно подтвердить в локальном Linux staging.
- [ ] Поэтому task 7.7 остается открытой до проверки загруженных CI artifacts,
  хотя локальные vulnerability/license/SBOM gates прошли.
- [ ] Поэтому общий task 8.9 остается открытым до зеленых OpenSpec, CI и hosted
  staging gates; workflow хранит SBOM/license artifacts для закрытия этих gates.
