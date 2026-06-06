<div align="center">

![Nexus-API](/web/default/public/logo.png)

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

- 使用最新 upstream new-api 作为干净底座
- 将 Nexus 变更作为模块化 downstream overlay 维护
- 避免冒用 upstream 商标、合作伙伴、赞助或部署声明
- 保持页面和代码风格贴近 upstream 原生实现
- 可见 UI 文案必须完整接入 i18n

## 关键下游功能

- 使用日志中的请求原模型/实际上游模型仅管理员和超级管理员可见
- 普通用户日志响应会移除 `admin_info`、`is_model_mapped`、`upstream_model_name`、`stream_status` 等管理字段
- Nexus 发行信息与 upstream new-api 最新版本信息分开展示
- Docker/GitHub Actions 配置使用 Nexus-API 自身发行身份时不得指向 upstream 镜像作为 Nexus 镜像

## 安装与部署

请根据本仓库实际构建产物和镜像标签部署 Nexus-API。若需要查看 upstream new-api 的安装文档，请访问 upstream 文档并注意其中的镜像、仓库和发布信息属于 upstream new-api。

## 许可证

本项目基于 AGPLv3 发布。通过网络向用户提供修改版本时，请遵守 AGPLv3 对源码提供和版权署名的要求。
