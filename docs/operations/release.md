# Выпуск и supply-chain проверки

Tag `v*` запускает `.github/workflows/release.yml`. Workflow выполняет нативный
smoke клиента на Linux, macOS и Windows, затем на Linux создает детерминированные
архивы `bord` для трех ОС и `bord-server` для Linux amd64. Версия, commit и UTC
build date встраиваются через ldflags; `SOURCE_DATE_EPOCH` равен времени commit.

`SHA256SUMS`, CycloneDX JSON SBOM исходников и распакованных release-бинарников,
отчеты `govulncheck`/Grype, license inventory и все архивы публикуются как CI
artifacts и assets GitHub Release. `govulncheck` блокирует выпуск при вызываемой
уязвимости; Grype независимо блокирует любой High/Critical module-level finding
в распакованных stripped-бинарниках, даже если call graph его не достигает. Отдельный `security.yml`
запускает `govulncheck`, запрещает forbidden/restricted licenses, создает
CycloneDX SBOM и сохраняет отчет лицензий. Любая вызываемая уязвимость или
запрещенная лицензия или High/Critical artifact finding блокирует release;
исключение требует отдельного review, документированной применимости и явного
policy-файла — сейчас таких исключений нет.

`modernc.org/mathutil v1.7.1` проверен вручную как BSD-3-Clause, поскольку
upstream module archive не содержит распознаваемого license-файла. Workflow
проверяет точную версию, а текст и источник зафиксированы в
`THIRD_PARTY_NOTICES` и `.github/license-policy/manual-review.csv`; изменение
версии ломает gate и требует нового review.

Локальная воспроизводимая проверка:

```sh
SOURCE_DATE_EPOCH="$(git show -s --format=%ct HEAD)" ./scripts/test-release.sh v0.0.0-test
```

Скрипт сам собирает набор дважды, сравнивает `SHA256SUMS`, безопасно проверяет
состав и встроенную версию всех четырех архивов и исполняет оба Linux-бинарника.
Чужие форматы проверяются без исполнения; официальный workflow дополнительно
выполняет нативный client smoke на трех runner ОС. До публикации tag проверьте
SBOM, license report и отсутствие High/Critical findings.

Локальные supply-chain команды (версии зафиксированы в workflow):

```sh
govulncheck ./...
go-licenses check ./... --disallowed_types=forbidden,restricted --ignore modernc.org/mathutil
go-licenses report ./... --ignore bord,modernc.org/mathutil > dependency-licenses.csv
cyclonedx-gomod mod -json -output source-bom.cdx.json
```

## Capacity gate

Перед выпуском выполните `./scripts/load-profile.sh /tmp/bord-load-profile.json`
на production-class staging host и приложите компактный JSON к release evidence.
Минимальный профиль и блокирующие пороги описаны в
[`docs/performance/README.md`](../performance/README.md). Любая ошибка, SQLite
 busy, пропущенная SSE delivery или превышение p95/memory/goroutine threshold
 блокирует выпуск; понижать пороги вместо расследования нельзя.
