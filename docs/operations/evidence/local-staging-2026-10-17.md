# Локальное evidence: operations/release

Секреты, bearer credentials, IP и пользовательские данные в отчет не включены.

| Проверка | Результат |
|---|---|
| Caddy | `v2.11.7`, config valid, HTTPS proxy smoke passed |
| SSE | heartbeat и resume-reconnect через HTTP/2 Caddy passed |
| Upgrade/rollback | `staging-old → staging-new → staging-old`, readiness passed |
| Backup/incident/restore | manifest verify, уничтожение данных, restore, logical thread и blob hash compare passed |
| Release | 4 архива, двукратная deterministic сборка, checksums/native Linux smoke passed |
| Vulnerabilities | `govulncheck v1.9.0`, 0 вызываемых vulnerabilities |
| Licenses | `go-licenses v1.6.0`, policy passed, 30 report rows |
| SBOM | CycloneDX JSON generated; SHA-256 `aeefa4362ba253a1778d8a7658c48c2a1e7fcd1fdddbd165478dc2d663792a45` |

Архивы `v0.0.0-local` (`SOURCE_DATE_EPOCH` из target commit):

```text
fba28cbc60b632d863a165a600594490f8c62176bea546c1b8563ebaf21622f3  bord-server_v0.0.0-local_linux_amd64.tar.gz
6d154311917d5cd64c85f4ecc698ec4fcc89d22bd99a3e06aa33d9a534c42d72  bord_v0.0.0-local_darwin_amd64.tar.gz
921677484c8b282ef2b8d056c5721f09621b99272abc6b695649de40aef6e9ac  bord_v0.0.0-local_linux_amd64.tar.gz
39815de66dda839d67b59198861b563d3318f8db83358dcf665ce43972628ac9  bord_v0.0.0-local_windows_amd64.zip
```

Детальные логи и сами локальные артефакты не коммитятся: они содержат абсолютные
пути и нестабильные timestamps. Воспроизведение описано командами в
`release.md`; staging — `CADDY_BIN=/path/to/caddy ./scripts/staging-smoke.sh`.
