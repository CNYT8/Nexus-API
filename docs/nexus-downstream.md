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

### System/version information

- Nexus release checks should use the Nexus-API release endpoint.
- The UI may also show the latest original new-api release separately.
- Version UI must follow the current upstream settings style in both default and classic frontends.

### i18n

- Every new visible UI string must use the existing i18n mechanism.
- Default frontend locale coverage: `en`, `zh`, `fr`, `ja`, `ru`, `vi`.
- Classic frontend locale coverage follows the current upstream classic locale set.

### Native style

- Reuse upstream components, spacing, routing, and service/model/controller patterns.
- Keep upstream-owned files as close to upstream as practical.
- Prefer small config/helper modules and narrow integration points over broad rewrites.

## Sync process

1. Record current Nexus state and downstream overlay status.
2. Use the latest upstream new-api as the clean base for large upstream rewrites.
3. Reapply the documented Nexus overlay in small modules.
4. Validate backend privacy, frontend admin/user behavior, i18n, docs/legal wording, and build/test checks.
5. Do not push, tag, or release unless explicitly requested.
