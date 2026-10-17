# Выпуск и supply-chain проверки

Tag `v*` запускает `.github/workflows/release.yml`. Workflow выполняет нативный
smoke клиента на Linux, macOS и Windows, затем на Linux создает детерминированные
архивы `bord` для трех ОС и `bord-server` для Linux amd64. Версия, commit и UTC
build date встраиваются через ldflags; `SOURCE_DATE_EPOCH` равен времени commit.

`SHA256SUMS`, SPDX JSON SBOM и все архивы публикуются как CI artifacts и assets
GitHub Release. Проверка critical vulnerability блокирует выпуск. Отдельный
`security.yml` запускает `govulncheck`, запрещает forbidden/restricted licenses,
создает CycloneDX SBOM и сохраняет отчет лицензий. Любая unresolved critical
уязвимость или запрещенная лицензия блокирует release; исключение требует
отдельного review и не добавляется молча в workflow.

`modernc.org/mathutil v1.7.1` проверен вручную как BSD-3-Clause, поскольку
upstream module archive не содержит распознаваемого license-файла. Workflow
проверяет точную версию, а текст и источник зафиксированы в
`THIRD_PARTY_NOTICES` и `.github/license-policy/manual-review.csv`; изменение
версии ломает gate и требует нового review.

Локальная воспроизводимая проверка:

```sh
SOURCE_DATE_EPOCH="$(git show -s --format=%ct HEAD)" ./scripts/test-release.sh v0.0.0-test
```

Повторите команду дважды на одном commit и сравните SHA-256 архивов. Для
официального выпуска проверьте install/smoke jobs всех трех runner ОС, SBOM,
license report и отсутствие critical finding до публикации tag.

## Capacity gate

Перед выпуском выполните `./scripts/load-profile.sh /tmp/bord-load-profile.json`
на production-class staging host и приложите компактный JSON к release evidence.
Минимальный профиль и блокирующие пороги описаны в
[`docs/performance/README.md`](../performance/README.md). Любая ошибка, SQLite
busy, пропущенная SSE delivery или превышение p95/memory/goroutine threshold
блокирует выпуск; понижать пороги вместо расследования нельзя.
