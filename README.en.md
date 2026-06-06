<div align="center">

![Nexus-API](/web/default/public/logo.png)

# Nexus-API

🍥 **A downstream AI API gateway build based on upstream new-api**

</div>

Nexus-API is an AGPLv3 downstream modification based on [new-api](https://github.com/QuantumNous/new-api). It keeps upstream capabilities while maintaining Nexus-specific release, deployment, and interface customizations.

## Upstream attribution

- Original project: [`QuantumNous/new-api`](https://github.com/QuantumNous/new-api)
- License: AGPLv3, see `LICENSE` and `NOTICE`
- Nexus-API is not an official upstream new-api release, partner, or endorsed service
- Copyright and attribution for upstream new-api, QuantumNous, and contributors remain preserved

## Nexus maintenance principles

- Use the latest upstream new-api as the clean base
- Maintain Nexus changes as a modular downstream overlay
- Do not misuse upstream trademarks, partner claims, sponsor claims, or deployment claims
- Keep UI and code style native to upstream implementation
- Wire all visible UI text through the existing i18n system

## Key downstream features

- Request/original upstream model information in usage logs is visible only to administrators and super administrators
- Normal user log responses remove management fields such as `admin_info`, `is_model_mapped`, `upstream_model_name`, and `stream_status`
- Nexus release information is displayed separately from the latest original new-api version
- Docker/GitHub Actions configuration must use Nexus-API identity when publishing Nexus artifacts and must not present upstream images as Nexus images

## Installation and deployment

Deploy Nexus-API using this repository's actual build artifacts and image tags. If you consult upstream new-api documentation, remember that upstream image names, repositories, and release information belong to upstream new-api.

## License

This project is licensed under AGPLv3. If you provide a modified version to users over a network, comply with AGPLv3 source availability and attribution requirements.
