# Безопасная operational telemetry

Канонический машинно-проверяемый список находится в
`internal/operations/telemetry`. Метрики используют только ограниченные наборы
значений: шаблон route вместо URL, класс статуса вместо полного ответа,
нормализованные reason/operation/scope. В labels запрещены request ID, resource
ID, session, hostname клиента, raw/HMAC IP, prefix и произвольный текст.

Лог-события могут содержать `request_id` для корреляции, но не тело поста,
credential, delete token, секрет, raw IP или HMAC IP. События retention содержат
только агрегированный count. Рекомендуемые alerts строятся по latency quantile,
доле SQLite busy, частоте SSE reset, media rejects и rate limits, а не по
отдельным источникам.
