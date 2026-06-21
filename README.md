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

- 使用本地 upstream new-api `v1.0.0-rc.12-4-g6ad5dbb6` 作为固定干净底座，不直接拉取远端最新版本
- 将 Nexus 变更作为模块化 downstream overlay 维护
- 避免冒用 upstream 商标、合作伙伴、赞助或部署声明
- 保持页面和代码风格贴近 upstream 原生实现
- 可见 UI 文案必须完整接入 i18n

## 安装与部署

Nexus-API 官方镜像：

```text
ghcr.io/cnyt8/nexus-api:latest
```

请部署上面的 Nexus-API 镜像，不要直接套用 upstream new-api 文档中的镜像名。

### Docker 快速部署

适合单机测试或轻量部署，数据持久化到当前目录的 `nexus-api-data` 和 `nexus-api-logs`：

```bash
mkdir -p nexus-api-data nexus-api-logs
docker pull ghcr.io/cnyt8/nexus-api:latest
docker run -d \
  --name nexus-api \
  --restart always \
  -p 3000:3000 \
  -v "$PWD/nexus-api-data:/data" \
  -v "$PWD/nexus-api-logs:/app/logs" \
  -e TZ=Asia/Shanghai \
  -e SESSION_SECRET="$(openssl rand -hex 32)" \
  ghcr.io/cnyt8/nexus-api:latest \
  --log-dir /app/logs
```

启动后访问：

```text
http://服务器IP:3000
```

查看日志和升级镜像：

```bash
docker logs -f nexus-api
docker pull ghcr.io/cnyt8/nexus-api:latest
docker stop nexus-api
docker rm nexus-api
```

删除容器不会删除 `nexus-api-data` 和 `nexus-api-logs`，重新执行上面的 `docker run` 指令即可使用新镜像启动。

### Docker Compose 部署

适合需要 PostgreSQL 和 Redis 的部署。生产环境请修改数据库密码、Redis 密码和 `SESSION_SECRET`。

```bash
mkdir -p nexus-api && cd nexus-api
cat > docker-compose.yml <<'YAML'
services:
  nexus-api:
    image: ghcr.io/cnyt8/nexus-api:latest
    container_name: nexus-api
    restart: always
    command: --log-dir /app/logs
    ports:
      - "3000:3000"
    volumes:
      - ./data:/data
      - ./logs:/app/logs
    environment:
      - SQL_DSN=postgresql://nexus:change_me@postgres:5432/nexus_api
      - REDIS_CONN_STRING=redis://:change_me@redis:6379
      - SESSION_SECRET=change_this_to_a_long_random_string
      - TZ=Asia/Shanghai
    depends_on:
      - postgres
      - redis

  postgres:
    image: postgres:15
    container_name: nexus-api-postgres
    restart: always
    environment:
      POSTGRES_USER: nexus
      POSTGRES_PASSWORD: change_me
      POSTGRES_DB: nexus_api
    volumes:
      - postgres-data:/var/lib/postgresql/data

  redis:
    image: redis:7
    container_name: nexus-api-redis
    restart: always
    command: ["redis-server", "--requirepass", "change_me"]

volumes:
  postgres-data:
YAML

docker compose up -d
```

常用维护指令：

```bash
docker compose logs -f nexus-api
docker compose pull
docker compose up -d
docker compose down
```

## 许可证

本项目基于 AGPLv3 发布。通过网络向用户提供修改版本时，请遵守 AGPLv3 对源码提供和版权署名的要求。
