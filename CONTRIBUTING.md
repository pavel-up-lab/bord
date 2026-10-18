# Участие в разработке Bord

Спасибо за желание помочь проекту. Обсуждения и код должны соответствовать
[`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md), а уязвимости следует передавать по
инструкции из [`SECURITY.md`](SECURITY.md).

## Локальная разработка

Требуется версия Go из `go.mod`. После клонирования запустите:

```sh
go mod download
make test
make contract
```

Полная проверка перед pull request:

```sh
make quality
make terminal-smoke
./scripts/test-release.sh
```

`make contract` повторно генерирует OpenAPI/sqlc-код и завершается ошибкой, если
generated-файлы отличаются от зафиксированных в Git.

## Pull request

1. Делайте одно логическое изменение в одном PR.
2. Добавляйте тест, воспроизводящий исправляемую ошибку или новое поведение.
3. Не добавляйте реальные IP, токены, базы, пользовательские данные и секреты.
4. Не ослабляйте лимиты, trusted proxy boundary, redaction и fail-closed
   проверки без отдельного security-обоснования.
5. Обновляйте README/OpenAPI/runbook, если изменилось публичное поведение.
6. Убедитесь, что рабочее дерево чистое после всех генераторов и тестов.

Go module намеренно называется `bord`: официальным способом распространения
являются checksummed архивы GitHub Releases, а не `go install` по module path.
