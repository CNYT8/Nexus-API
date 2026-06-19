# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Repository identity and current status

- This repository is **Nexus-API**, an AGPLv3 downstream fork / overlay of upstream [`QuantumNous/new-api`](https://github.com/QuantumNous/new-api). Keep upstream attribution, licenses, NOTICE, and protected upstream identity visible where required.
- Main working repository: `/root/ClaudeCode/Nexus-API`.
- Read-only upstream reference mirror: `/root/ClaudeCode/new-api`. Use it to compare behavior; do **not** modify it.
- Git remote: `origin https://github.com/CNYT8/Nexus-API.git`.
- Current released version: `v1.0.0` (`VERSION` contains `v1.0.0`). The first Docker release was built by GitHub Actions and pushed to:
  - `ghcr.io/cnyt8/nexus-api:v1.0.0`
  - `ghcr.io/cnyt8/nexus-api:latest`
  - manifest digest recorded from the successful run: `sha256:7a6e1315b73b8ec4b9d2a3b9faa65ca679fa13f5964f5c2440791411763b490a`
- Release/build history to remember: v1.0.0 tag points at commit `caf4664` (`rc.11 下游审查修复...`). GitHub Actions Docker build run `27618516728` succeeded after an earlier GHCR package permission failure.
- A complete GitLab CI draft exists at `/root/.gitlab-ci.yml`; it is intentionally outside the repo unless the user asks to move it in.

## Non-negotiable user/project constraints

- Upstream `new-api` is the authoritative behavior reference. Review and change Nexus code as downstream modifications around the user's second-pass changes; do not blame or rewrite upstream behavior unless there is a Nexus-specific reason.
- The upstream new-api base is fixed to `v1.0.0-rc.11`; do not fetch or infer the latest upstream version for the “based on new-api” version field or large-sync base.
- Preserve a modular downstream overlay. Prefer narrow, add-only or small integration patches that can be reapplied after upstream syncs. Avoid broad file copies, wholesale rewrites, or importing upstream git history.
- **Never import upstream git history into this repo**: do not merge/rebase/reset from upstream. Sync file contents or individual patches only.
- Git authorship / contributor constraint is highest priority: contributors must be only the user. If committing, use the user's identity (`CNYT <ldh14891@gmail.com>`) and do **not** add Claude/Anthropic attribution, co-author trailers, or generated-by trailers. Do not include `🤖 Generated with Claude Code` anywhere in commits/PRs; `.github/workflows/pr-check.yml` blocks that phrase.
- Confirm before hard-to-reverse or outward-facing actions: deleting files, overwriting user work, pushing, tagging, publishing releases/images, triggering Actions, opening PRs, or changing package visibility.
- Do not push, tag, release, run deployment/publish workflows, or create PRs unless the user explicitly asks in the current context.
- UI changes must feel native to the original team style: reuse existing Semi/default components, spacing, cards, tags, and table/detail patterns. No AI-looking add-on styling.
- Every visible UI text change must be wired into the existing i18n system across all supported locales before completion.
- For classic frontend locale changes, update all 8 classic locales: `en`, `zh`, `zh-CN`, `zh-TW`, `fr`, `ja`, `ru`, `vi`. Insert targeted keys near related entries; do not rewrite whole JSON files with `json.dump`.
- For default frontend locale changes, update the current default locale set: `en`, `zh`, `fr`, `ja`, `ru`, `vi`.
- Never infer visible/runtime versions from branch names or intent; verify the actual artifact (`VERSION`, built image tag, running app, or release data).
- When cloning or moving UI components, preserve the parent mount/remount contract. If a child uses `useState` lazy initialization from props, verify the save -> refresh -> redisplay round trip.
- Project requirements to preserve:
  1. Strict modularity and strict maintainability in structure.
  2. Code style should closely mimic the original team and must not feel like an add-on.
  3. Reuse UI that can be applied 1:1, including existing switch/button patterns.
  4. This numbered requirement block is part of the project requirements and must not lose its format.

## Common commands

Run commands from `/root/ClaudeCode/Nexus-API` unless noted.

### Backend / Go

```bash
# Format touched Go files
gofmt -w path/to/file.go

# Run all Go tests (requires embedded frontend dist directories to exist for package main)
go test ./...

# Run focused package tests
go test ./relay/helper/... ./dto/...
go test ./relay/channel/openai -run TestOpenAIImageStream -count=1

# Run one exact test in one package
go test ./model -run '^TestName$' -count=1

# Build backend after both frontend dist folders exist
go build -ldflags "-s -w -X 'github.com/QuantumNous/new-api/common.Version=$(cat VERSION)'" -o new-api
```

`main.go` embeds both `web/default/dist` and `web/classic/dist`; backend package builds/tests that include package `main` fail if those dist directories are missing. Build both frontends first, or run focused non-main package tests while iterating.

### Frontend / Bun workspace

```bash
# Install workspace dependencies
cd web && bun install --frozen-lockfile

# Build default frontend
cd web/default && DISABLE_ESLINT_PLUGIN=true VITE_REACT_APP_VERSION=$(cat ../../VERSION) bun run build

# Build classic frontend (Nexus ships/targets classic)
cd web/classic && VITE_REACT_APP_VERSION=$(cat ../../VERSION) bun run build

# Default frontend checks
cd web/default && bun run typecheck
cd web/default && bun run lint
cd web/default && bun run format:check
cd web/default && bun run i18n:sync

# Classic frontend checks
cd web/classic && bun run build
cd web/classic && bun run lint
cd web/classic && bun run eslint
```

### Docker / cloud build

```bash
# Local Docker build, if local machine has enough resources
docker build -t nexus-api:local .

# Cloud Docker image build/push via GitHub Actions, only after explicit user approval
gh workflow run docker-build.yml --ref v1.0.0 -f image_tag=v1.0.0
```

The Dockerfile builds both frontends with Bun, then builds the Go binary in `golang:1.26.1-alpine`, then copies it into a Debian slim runtime image.

## High-level architecture

- Backend is layered: `router/` -> `controller/` -> `service/` -> `model/`.
- `main.go` initializes options, DB/cache, background jobs, routers, and embeds both frontends.
- `model/` owns GORM models, migrations, query helpers, and DB compatibility logic. The app supports SQLite, MySQL, and PostgreSQL simultaneously.
- `common/` contains shared runtime globals, env/config parsing, JSON wrappers, crypto, cache helpers, and request/body utilities.
- `setting/` registers configurable option groups. Boolean/string options generally flow through `common.OptionMap`, `model/option.go`, and periodic `model.SyncOptions`.
- Relay flow: auth/distribution middleware sets context -> `relay/common.RelayInfo` is generated -> provider adapter under `relay/channel/*` handles upstream format -> quota/logging services settle usage -> `model/log.go` persists logs.
- `dto/` request structs are used for client JSON and upstream remarshal. Optional scalar relay request fields must use pointer types with `omitempty` so explicit zero/false survives remarshal.
- Frontend is a Bun workspace in `web/` with `web/default` (React 19/Rsbuild/Base UI/Tailwind) and `web/classic` (React/Semi style). Nexus currently ships and customizes the classic frontend while still building default for embedding.
- `docs/nexus-downstream.md` is the authoritative downstream overlay map. Read it before changing Nexus-specific behavior or syncing from upstream.

## Existing project rules to preserve

### JSON wrapper rule

Business code must use `common/json.go` wrappers for JSON marshal/unmarshal/decode operations:

- `common.Marshal`
- `common.Unmarshal`
- `common.UnmarshalJsonStr`
- `common.DecodeJson`
- `common.GetJsonType`

Do not add direct `encoding/json` marshal/unmarshal calls in business code. Type references such as `json.RawMessage` are acceptable when needed.

### Cross-database rule

All DB code must work on SQLite, MySQL >= 5.7.8, and PostgreSQL >= 9.6. Prefer GORM. If raw SQL is unavoidable, account for quoting and booleans:

- PostgreSQL quotes columns as `"column"`; MySQL/SQLite use backticks.
- Reserved columns such as `group` and `key` should use existing helpers such as `commonGroupCol` / `commonKeyCol` in `model/main.go`.
- Use `commonTrueVal` / `commonFalseVal` and `common.UsingPostgreSQL`, `common.UsingSQLite`, `common.UsingMySQL` where DB-specific branching is required.

### Protected upstream identity

Do not remove, hide, or rewrite upstream new-api / QuantumNous attribution, license, module path, copyright notices, or required documentation. Nexus may describe itself as a downstream modified project, but must not claim to be upstream official, endorsed, sponsored, or partnered.

### Billing expression system

Before changing tiered/dynamic billing expression logic, read `pkg/billingexpr/expr.md`. It documents variables, token normalization, editor -> storage -> pre-consume -> settlement -> log display, quota conversion, and expression versioning.

### StreamOptions rule

When implementing or changing a channel, confirm whether that provider supports `StreamOptions`. If it does, add or keep the channel in `streamSupportedChannels`.

## Nexus downstream overlay details

### Branding / release overlay

- README presents Nexus-API as a downstream modified project based on upstream new-api.
- Release and Docker references for Nexus should point to Nexus resources, while upstream docs/links should be clearly labeled as upstream/original resources.
- `docker-build.yml` publishes to `ghcr.io/cnyt8/nexus-api` with `workflow_dispatch` input `image_tag`.

### rc.11 review and fixes already completed

The rc.11 downstream review focused only on Nexus second-pass modifications and treated upstream new-api as correct. Fixes already made include:

- `service/http_client.go`: restored `IdleConnTimeout` on the default transport while preserving proxy/env and HTTP/2 behavior.
- `relay/channel/openai/adaptor.go` and `dto/openai_request.go`: restored upstream precise OpenAI reasoning/GPT-5 model helpers instead of broad `HasPrefix("o")` matching.
- `model/log.go`: fixed `resolveRecordIpLog` same-second last-writer-wins semantics with `>=` plus timestamp > 0.
- `controller/user.go`: every personal setting submit refreshes `RecordIpLogUpdatedAt`, allowing users to re-confirm the same value after a global default change.
- `controller/option.go`: changing `DefaultRecordIpLogEnabled` writes `DefaultRecordIpLogUpdatedAt` first and fails closed if that write fails.
- Several Go files were gofmt-aligned after the review.

### User IP and log-IP recording overlay

- `model.User` includes `register_ip`, `last_login_ip`, and `last_api_ip` columns. GORM auto-migrates them.
- Register IP is captured in the registration funnel; login IP is captured in `setupLogin`; API IP is captured through `model.RecordUserLastApiIp` in `TokenAuth` and in the playground handler because playground bypasses `TokenAuth`.
- `controller/playground.go` records `model.RecordUserLastApiIp(userId, c.ClientIP())` after reading the session user id.
- Admin `EditUserModal` shows an admin-only IP information card using native `Tag shape='circle'` click-to-copy styling.
- `DefaultRecordIpLogEnabled` controls whether request/error logs record IP by default. It uses `DefaultRecordIpLogUpdatedAt` and per-user `RecordIpLogUpdatedAt` last-writer-wins semantics in `model.resolveRecordIpLog`; it does not bulk-update all user rows. `DefaultRecordIpLogForced` disables user-side adjustment and makes log writes use the admin default directly.
- Turnstile is stored in the `options` table as key `TurnstileCheckEnabled` with string value `true`/`false`. To disable directly in PostgreSQL: `UPDATE options SET value = 'false' WHERE key = 'TurnstileCheckEnabled';` then restart the app because the bool is cached in memory.

### Audit and usage logs

- `model.Log` stores `UserId`, `Username`, `TokenName`, `TokenId`, `ChannelId`, `Group`, `Ip`, request IDs, and JSON `Other`.
- Normal user log responses must strip admin-only/debug fields in `formatUserLogs`: `admin_info`, `audit_info`, stream status, model mapping/original/upstream model keys, etc.
- `admin_info` is not always an operator. In consumption/error logs it often contains channel/admin-debug data such as `use_channel`, `is_multi_key`, `multi_key_index`, local count token flags, and channel affinity data. Only treat it as an operator when `admin_id` or `admin_username` exists.
- Upstream classic already has an admin-only `操作管理员` row for management logs. Avoid broad UI logic that displays `操作者: - - [未知]` merely because `other.admin_info` exists.
- Multi-key channel index is displayed in the classic usage-log detail expansion as admin-only `密钥信息` with 1-based `#N`, matching the multi-key management UI. Backend already populates `admin_info.is_multi_key` and `admin_info.multi_key_index`.

### Classic frontend / i18n overlay

- Nexus's visible downstream UI changes are in `web/classic` unless a task explicitly targets `web/default`.
- Classic usage-log UI includes log type 7 (`登录`), structured operation display (`操作`, `操作参数`), login method, admin-only auth/operator details, model mapping/original/upstream model display, and multi-key key information.
- All classic visible strings must exist in all 8 locale JSON files. Do localized additions surgically near related keys.

## Git, upload, and PR requirements

- Before any commit, check `git status --short --branch` and inspect changed files. Do not commit unrelated or user-created changes accidentally.
- Do not commit or push until the user asks. If the user asks to upload/release, confirm exact target actions if destructive/outward-facing details are ambiguous.
- Commit messages must not include Claude/Anthropic co-author trailers or AI-generated footers. The user's no-extra-contributors rule overrides generic upstream PR attribution guidance for this downstream repo.
- If creating a PR, use `.github/PULL_REQUEST_TEMPLATE.md` and write a concise human-style summary. Do not paste raw AI output. The PR check requires the template and blocks obvious AI-slop markers.
- Do not move tags that already exist on the remote without explicit confirmation. The previous local `v1.0.0` tag was safely recreated only because the remote had no such tag at that time.
- GitHub Actions Docker publishing requires GHCR package write permission; the current `docker-build.yml` has `packages: write`.

## Verification expectations

- For backend-only changes: run focused `go test` packages covering the touched code, then broader `go test ./...` if frontend dist exists.
- For relay/request DTO changes: include tests that prove explicit zero/false values are preserved and upstream behavior matches new-api.
- For frontend changes: build the targeted frontend (`web/classic` for Nexus UI work) and verify no raw i18n keys appear.
- For IP/log privacy work: verify both admin and normal-user log views. Normal-user APIs must not receive admin-only fields.
- For save/refresh UI behavior: verify the actual round trip, not just state mutation before refresh.
- For release/Docker work: prefer cloud GitHub Actions when local resources are insufficient; report exact run id, status, image tags, and digest.
