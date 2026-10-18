# Публикация проекта на GitHub

## Создание репозитория

В интерфейсе GitHub создайте новый **пустой** репозиторий: не добавляйте README,
`.gitignore` или LICENSE, поскольку они уже находятся в истории проекта.

После создания подключите только ветку `main`:

```sh
git remote add origin git@github.com:<OWNER>/<REPOSITORY>.git
git push -u origin main
```

Не используйте `git push --all`: локальные merged-ветки предыдущих этапов не
нужны в публичном репозитории.

## Настройки GitHub

1. В **Settings → Actions → General** разрешите Actions для репозитория.
2. В **Settings → Code security** включите Dependency graph, Dependabot alerts,
   Dependabot security updates и Private vulnerability reporting.
3. Защитите `main`: обязательный pull request и успешные workflows
   `foundation`, `security`, `terminal-smoke`.
4. Добавьте описание проекта, ссылку `https://bord.pw` и topics:
   `go`, `tui`, `imageboard`, `bubbletea`, `sqlite`, `terminal`.

## Первый релиз

Сначала дождитесь зелёных Actions на `main`. Затем в интерфейсе GitHub создайте
Release с новым тегом стабильной версии, например `v0.1.1`, на commit из
`main`. Tag push запустит `release.yml`: native smoke на Linux/macOS/Windows,
security/license gates, SBOM, checksums и публикацию архивов.

Не отмечайте релиз готовым, пока OpenSpec-задачи 7.7 и 8.9 не подтверждены
результатами hosted workflow.
