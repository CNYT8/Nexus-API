# Nexus-API downstream overlay

Nexus-API is maintained as a downstream overlay on top of upstream new-api. For large upstream rewrites, rebuild from the latest upstream base and reapply the overlay described here instead of relying on memory or broad file copies.

## Preservation checklist

### Branding and attribution

- Nexus-API may describe itself as a modified downstream project based on new-api.
- Upstream new-api, QuantumNous contributors, AGPLv3, NOTICE, and third-party license attribution must remain visible where required.
- Nexus-API must not claim to be the upstream official project, partner, sponsor, endorsed service, or upstream deployment.

### README and public docs

- README files should use Nexus-API as the fork identity.
- Docker and release examples should point to Nexus-API resources when they are Nexus instructions.
- Upstream documentation and repository links should be labeled as upstream/original resources.

### Usage-log original/upstream model privacy

- Normal user log responses must not contain `admin_info`, `stream_status`, `is_model_mapped`, `upstream_model_name`, or legacy original/upstream model keys.
- Admin and super-admin log views may show request model and actual/upstream model for mapped logs.
- Frontend visibility must follow the same permission style as channel/admin info: admin UI can render it, normal user UI cannot receive or render it.
- Multi-key channel index (`admin_info.multi_key_index`) is rendered in the log detail expansion as an admin-only "密钥信息" row, displayed `#N` (1-based, matching the multi-key management UI). It lives inside the existing admin-only channel-info block in `web/classic/src/hooks/usage-logs/useUsageLogsData.jsx`; backend already populates `admin_info` so no backend change is required.

### System/version information

- Nexus release checks should use the Nexus-API release endpoint.
- The UI may also show the latest original new-api release separately.
- Version UI must follow the current upstream settings style in both default and classic frontends.

### User IP recording

- The User model carries `register_ip`, `last_login_ip`, `last_api_ip` columns (GORM auto-migrates them; no manual migration).
- Register IP is captured at the registration funnel; login IP via `setupLogin`; API IP via `model.RecordUserLastApiIp` (throttled with an in-process `sync.Map`) from `TokenAuth` and from the playground handler (`controller/playground.go`), since the playground bypasses `TokenAuth`.
- Admin EditUserModal shows the three IPs in an admin-only "IP 信息" card, values rendered as click-to-copy `Tag shape='circle'` (the codebase IP idiom).
- Operation setting `DefaultRecordIpLogEnabled` uses read-path last-writer-wins, not bulk overwrite: `common.DefaultRecordIpLogUpdatedAt` is bumped whenever the admin toggle's value changes, and each user's `setting.record_ip_log_updated_at` is bumped whenever they change their own switch. `model.resolveRecordIpLog` (called from both write sites in `model/log.go`) uses the user's value when their timestamp is newer, otherwise the global default. New users keep `record_ip_log_updated_at = 0` so they naturally follow the global — `model/user.go`'s `Insert`/`InsertWithTx` stay byte-identical with upstream, and there is no full-table write.

### i18n

- Every new visible UI string must use the existing i18n mechanism.
- Default frontend locale coverage: `en`, `zh`, `fr`, `ja`, `ru`, `vi`.
- Classic frontend locale coverage follows the current upstream classic locale set.

### Native style

- Reuse upstream components, spacing, routing, and service/model/controller patterns.
- Keep upstream-owned files as close to upstream as practical.
- Prefer small config/helper modules and narrow integration points over broad rewrites.

## rc.11 sync notes (v1.0.0-rc.11)

Synced as modular, add-only-where-possible patches. High-value backend/security/perf updates were taken; the upstream `web/default/` rewrites (data-table refactor, model-pricing visual editor, shared dialog/json-editor) were intentionally skipped because Nexus carries its own downstream `web/default/src/components/data-table/*` overlay and ships the classic frontend.

Synced:

- Anonymous request body limit (security): `common/request_body_limit.go` + `middleware/request_body_limit.go` (add-only), `constant.AnonymousRequestBodyLimitKB` env (`ANONYMOUS_REQUEST_BODY_LIMIT_KB`, default 512KB), wired onto unauthenticated POST routes in `router/api-router.go` via a shared `anonymousRequestBodyLimit` handler.
- `RelayIdleConnTimeout` (`RELAY_IDLE_CONN_TIMEOUT`, default 90s) added to `common/constants.go` + `common/init.go` and applied to every `http.Transport` in `service/http_client.go`.
- Stream scanner perf rewrite: `relay/helper/stream_scanner.go` (`NewStreamScanner`, 128MiB fallback buffer) + `stream_scanner_test.go`.
- OpenAI image streaming (PR #4608): added `relay/channel/openai/relay_image.go` (`OpenaiImageHandler` / `OpenaiImageStreamHandler` / `OpenaiImageJSONAsStreamHandler` / `normalizeOpenAIUsage`), restored `ImageRequest.Stream *bool` + `IsStream` in `dto/openai_image.go`, ported reusable multipart parsing (`common.ParseMultipartFormReusable`) into `relay/helper/valid_request.go`, routed image generations/edits to the stream handler when `info.IsStream` in `relay/channel/openai/adaptor.go`, and renamed the xAI call site to `openai.OpenaiImageHandler`. The legacy `OpenaiHandlerWithUsage` (and the older `OpenaiRealtimeHandler` / `usage.go` helpers) are kept in `relay-openai.go` rather than re-split, so upstream `usage.go` / `relay_realtime.go` were NOT copied (avoids duplicate symbols). Tests added: `image_stream_test.go`, `image_edit_test.go`, `relay/helper/openai_image_request_test.go`.
- Audit logging (security/observability, PR #5462): added `controller/audit.go` + `middleware/audit.go` (add-only), `constant.ContextKeyAuditLogged`, `model.LogTypeLogin = 7`, and `buildOpField` / `RecordLoginLog` / `RecordOperationAuditLog` in `model/log.go`. The admin/root write-audit fallback is wired into `authHelper` in `middleware/auth.go` (begin/finish around `c.Next()`). Manage call sites in `controller/user.go` (login, user create/update/delete, binding clear, quota add/subtract/override, manage) and `controller/option.go` (`option.update`) emit structured audits.
- `SearchUsers` deleted-user status filter (`status == -1` → soft-deleted) ported into `model/user.go` while preserving the Nexus IP columns/functions.
- Classic audit-log UI: `UsageLogsColumnDefs.jsx` renders log type 7 as "登录"; `useUsageLogsData.jsx` shows structured `op.action` / `op.params`, login method, and admin-only operator + auth method, with 6 new i18n keys (`登录`/`操作`/`操作参数`/`登录方式`/`操作者`/`认证方式`) added across all 8 classic locales.

Audit vs IP-recording coexistence (do not regress):

- rc.11 audit logging is additive. Nexus keeps `model.RecordUserLastApiIp` in `middleware/auth.go` `TokenAuth` and in `controller/playground.go`, keeps `RegisterIp`/`LastLoginIp`/`LastApiIp` columns, and keeps `UpdateUserLastLoginIp` in the login flow (recorded before session save; `recordLoginAudit` runs after session save).
- `formatUserLogs` strips both `admin_info` and the new `audit_info` for normal users, on top of the existing model-mapping privacy deletes.
- The record-IP read path stays `model.resolveRecordIpLog(userId)` (last-writer-wins), not the upstream per-user-only read.

Skipped (high-cost / low-ROI for Nexus):

- All `web/default/` upstream rewrites (data-table, model-pricing editor, shared dialog/json-editor/provider-badge, default localized audit renderer).
- Codex channel chore (Nexus has its own `controller/codex_oauth.go` + `CodexOAuthModal.jsx`), issue templates, and `Footer.jsx` (Nexus has its own attribution).

## Sync process

1. Record current Nexus state and downstream overlay status.
2. Use the latest upstream new-api as the clean base for large upstream rewrites.
3. Reapply the documented Nexus overlay in small modules.
4. Validate backend privacy, frontend admin/user behavior, i18n, docs/legal wording, and build/test checks.
5. Do not push, tag, or release unless explicitly requested.
