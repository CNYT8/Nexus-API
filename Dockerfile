FROM rust:1.75-bookworm AS builder
WORKDIR /app
COPY . .
RUN cargo build --release

FROM debian:bookworm-slim
WORKDIR /app
RUN apt-get update && apt-get install -y ca-certificates tzdata && rm -rf /var/lib/apt/lists/*
COPY --from=builder /app/target/release/nexus-api /app/nexus-api
RUN mkdir -p /app/web/dist
EXPOSE 3000
CMD ["/app/nexus-api"]
