<div align="center">

# Nexus-API

🍥 **基于 upstream new-api 的下游修改版 AI API 网关**

</div>

Nexus-API 是基于 [new-api](https://github.com/QuantumNous/new-api) 的 AGPLv3 下游修改项目，用于在保留 upstream 能力的基础上维护 Nexus 发行、部署和界面定制。

## 上游归属

- 原项目：[`QuantumNous/new-api`](https://github.com/QuantumNous/new-api)
- 许可证：AGPLv3，详见本仓库 `LICENSE` 与 `NOTICE`
- Nexus-API 不是 upstream new-api 的官方发布、合作伙伴或背书服务
- upstream new-api、QuantumNous 与相关贡献者的版权和署名保持不变

## Nexus 维护原则

- 使用 upstream new-api `v1.0.0-rc.12` 作為固定乾淨底座，不直接拉取遠端最新版本
- 将 Nexus 变更作为模块化 downstream overlay 维护
- 避免冒用 upstream 商标、合作伙伴、赞助或部署声明
- 保持页面和代码风格贴近 upstream 原生实现
- 可见 UI 文案必须完整接入 i18n

## 安装与部署

请根据本仓库实际构建产物和镜像标签部署 Nexus-API。若需要查看 upstream new-api 的安装文档，请访问 upstream 文档并注意其中的镜像、仓库和发布信息属于 upstream new-api。

## 许可证

本项目基于 AGPLv3 发布。通过网络向用户提供修改版本时，请遵守 AGPLv3 对源码提供和版权署名的要求。
