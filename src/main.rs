use axum::{
    routing::{get, post},
    Router, Json, middleware as axum_middleware,
};
use serde_json::{json, Value};
use sqlx::{mysql::MySqlPoolOptions, MySqlPool};
use tokio::net::TcpListener;
use tower_http::{services::ServeDir, cors::{CorsLayer, Any}};
use redis::aio::MultiplexedConnection;

pub mod models;
pub mod crypto;
pub mod middleware;
pub mod billing;
pub mod relay;

#[derive(Clone)]
pub struct AppState {
    pub db: MySqlPool,
    pub redis: MultiplexedConnection,
    pub http_client: reqwest::Client,
}

fn parse_dsn(dsn: &str) -> String {
    let base = dsn.split('?').next().unwrap_or(dsn);
    if base.starts_with("mysql://") {
        return base.to_string();
    }
    let parts: Vec<&str> = base.split("@tcp(").collect();
    if parts.len() == 2 {
        let creds = parts[0];
        let rest: Vec<&str> = parts[1].split(")/").collect();
        if rest.len() == 2 {
            return format!("mysql://{}@{}/{}", creds, rest[0], rest[1]);
        }
    }
    base.to_string()
}

#[tokio::main]
async fn main() {
    let _ = dotenvy::dotenv();
    tracing_subscriber::fmt::init();

    let raw_dsn = std::env::var("SQL_DSN")
        .unwrap_or_else(|_| "root:123456@tcp(127.0.0.1:3306)/newapi".to_string());
    let db_url = parse_dsn(&raw_dsn);
    
    let pool = MySqlPoolOptions::new()
        .max_connections(1000)
        .connect(&db_url)
        .await
        .expect("Database connection failed");

    let redis_url = std::env::var("REDIS_URL")
        .unwrap_or_else(|_| "redis://127.0.0.1:6379".to_string());
    let redis_client = redis::Client::open(redis_url).expect("Invalid Redis URL");
    let redis_conn = redis_client.get_multiplexed_async_connection().await.expect("Redis connection failed");

    let http_client = reqwest::Client::builder()
        .pool_max_idle_per_host(500)
        .build()
        .unwrap();

    let state = AppState { 
        db: pool, 
        redis: redis_conn,
        http_client,
    };

    let cors = CorsLayer::new()
        .allow_origin(Any)
        .allow_methods(Any)
        .allow_headers(Any);

    let app = Router::new()
        .route("/api/status", get(api_status))
        .route(
            "/v1/chat/completions", 
            post(relay::chat_completions)
            .route_layer(axum_middleware::from_fn_with_state(state.clone(), middleware::auth_middleware))
        )
        .fallback_service(ServeDir::new("web/dist"))
        .layer(cors)
        .with_state(state);

    let listener = TcpListener::bind("0.0.0.0:3000").await.unwrap();
    tracing::info!("Server listening on 0.0.0.0:3000");
    axum::serve(listener, app).await.unwrap();
}

async fn api_status() -> Json<Value> {
    Json(json!({
        "success": true,
        "message": "",
        "data": {
            "version": "v1.0.0",
            "system_name": "Nexus",
            "turnstile_site_key": "",
            "wechat_login": false,
            "github_oauth": true,
            "quota_per_unit": 500000,
            "HeaderNavModules": "{\"pricing\":{\"requireAuth\":false}}"
        }
    }))
}
