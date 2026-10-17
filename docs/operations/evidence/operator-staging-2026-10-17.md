# Operator commands: clean staging, 2026-10-17

Запуск: `./scripts/operator-staging-smoke.sh` из чистого временного каталога.
Credential/key values не печатались; временный путь ниже сокращен до `$STAGE`.

```text
> migrate
{"current":3,"maximum_supported":3,"state":"ready"}
> status
{"current":3,"maximum_supported":3,"state":"ready"}
> admin-create
administrator create completed id=staging-admin credential_file=$STAGE/admin.credential
> admin-rotate
administrator rotate completed id=staging-admin credential_file=$STAGE/admin.next
> admin-revoke
administrator revoked id=staging-admin
> moderator-create
administrator create completed id=staging-moderator credential_file=$STAGE/moderator.credential
> admin-ban
ban created id=00000001
> admin-unban
ban revoked id=00000001
> retention-hold
legal hold hold subject_id=1
> retention-release
legal hold release subject_id=1
> retention-report
{"EligibleIP":0,"LegalHoldIP":0,"ActiveBanIP":0,"Idempotency":0,"Challenges":0}
> retention-run
{"EligibleIP":0,"LegalHoldIP":0,"ActiveBanIP":0,"Idempotency":0,"Challenges":0}
> reencrypt
{"Reencrypted":0,"Remaining":0}
> verify
restore verification passed manifest_format=1 files=1
> restore
restore completed; run status and readiness checks before cutover
> restored-status
{"current":3,"maximum_supported":3,"state":"ready"}
operator staging smoke passed (secrets omitted)
```

Дополнительно smoke проверил отказ `admin revoke` без `--confirm REVOKE` и режим
`0600` credential-файла. Тест `TestReencryptRoundTripAndResume` выполняет
реальный AES-GCM round-trip и повторный resume; `TestRetentionHonorsHoldAndActiveBan`
проверяет оба исключения retention.
