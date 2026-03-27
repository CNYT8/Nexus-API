# 第一阶段：极速编译 React 前端
FROM node:18-alpine AS frontend-builder
WORKDIR /web
COPY web/package.json ./
RUN npm install
COPY web/ ./
RUN npm run build

# 第二阶段：极限编译 Rust 后端
FROM rust:1.75-bookworm AS backend-builder
WORKDIR /app
COPY Cargo.toml ./
COPY src/ ./src/
RUN cargo build --release

# 第三阶段：组装最终的轻量级生产镜像
FROM debian:bookworm-slim
WORKDIR /app
# 安装必要的系统底层库和 CA 证书
RUN apt-get update && apt-get install -y ca-certificates tzdata && rm -rf /var/lib/apt/lists/*
# 将 Rust 编译好的引擎核心拿过来
COPY --from=backend-builder /app/target/release/nexus-api /app/nexus-api
# 将 Node.js 编译好的绝美静态前端页面拿过来
COPY --from=frontend-builder /web/dist /app/web/dist

EXPOSE 3000
CMD ["/app/nexus-api"]
