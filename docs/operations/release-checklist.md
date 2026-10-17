# MVP release checklist

Дата локальной проверки: 2026-10-17. Целевой commit: `6b935f997a35`.

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

## Внешние и продуктовые blockers

- [ ] Admin bootstrap/revoke, ручной retention и re-encryption key rotation не
  подключены к production `bord-server`; task 7.5 остается открытой.
- [ ] OpenSpec tasks 8.4–8.6 и 8.8 принадлежат другим проверкам и еще открыты.
- [ ] Hosted GitHub Actions, GitHub Release assets, публичный DNS/ACME и нативное
  исполнение macOS/Windows нельзя честно подтвердить в локальном Linux staging.
- [ ] Поэтому task 7.7 остается открытой до проверки загруженных CI artifacts,
  хотя локальные vulnerability/license/SBOM gates прошли; точные hashes, версии
  и hosted blocker записаны в `evidence/supply-chain-a00cb00.md`.
- [ ] Поэтому общий task 8.9 остается открытым до зеленых OpenSpec, CI и hosted
  staging gates; workflow хранит SBOM/license artifacts для закрытия этих gates.
