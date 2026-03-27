use axum::{routing::{get, post}, Router, Json, middleware as axum_middleware};
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
pub mod auth;
pub mod channel;
pub mod user;

#[derive(Clone)]
pub struct AppState {
    pub db: MySqlPool,
    pub redis: MultiplexedConnection,
    pub http_client: reqwest::Client,
}

fn parse_dsn(dsn: &str) -> String {
    let base = dsn.split('?').next().unwrap_or(dsn);
    if base.starts_with("mysql://") { return base.to_string(); }
    let parts: Vec<&str> = base.split("@tcp(").collect();
    if parts.len() == 2 {
        let creds = parts[0];
        let rest: Vec<&str> = parts[1].split(")/").collect();
        if rest.len() == 2 { return format!("mysql://{}@{}/{}", creds, rest[0], rest[1]); }
    }
    base.to_string()
}

async fn init_db(pool: &MySqlPool) {
    let _ = sqlx::query("CREATE TABLE IF NOT EXISTS users (id INT AUTO_INCREMENT PRIMARY KEY, username VARCHAR(255) NOT NULL UNIQUE, password VARCHAR(255) NOT NULL, display_name VARCHAR(255), role INT DEFAULT 1, status INT DEFAULT 1, quota INT DEFAULT 0, used_quota INT DEFAULT 0, `group` VARCHAR(64) DEFAULT 'default', aff_code VARCHAR(64) UNIQUE, deleted_at DATETIME(3) DEFAULT NULL);").execute(pool).await;
    let _ = sqlx::query("CREATE TABLE IF NOT EXISTS tokens (id INT AUTO_INCREMENT PRIMARY KEY, user_id INT NOT NULL, `key` CHAR(48) NOT NULL UNIQUE, status INT DEFAULT 1, name VARCHAR(255), created_time BIGINT, expired_time BIGINT DEFAULT -1, remain_quota INT DEFAULT 0, unlimited_quota TINYINT(1) DEFAULT 0, deleted_at DATETIME(3) DEFAULT NULL);").execute(pool).await;
    let _ = sqlx::query("CREATE TABLE IF NOT EXISTS logs (id BIGINT AUTO_INCREMENT PRIMARY KEY, user_id INT NOT NULL, created_at BIGINT, type INT, content TEXT, quota INT, prompt_tokens INT, completion_tokens INT, use_time INT, is_stream TINYINT(1), model_name VARCHAR(255), token_name VARCHAR(255), channel_id INT);").execute(pool).await;
}

#[tokio::main]
async fn main() {
    let _ = dotenvy::dotenv();
    let raw_dsn = std::env::var("SQL_DSN").unwrap_or_else(|_| "root:123456@tcp(127.0.0.1:3306)/newapi".to_string());
    let db_url = parse_dsn(&raw_dsn);
    
    let mut retries = 5;
    let pool = loop {
        match MySqlPoolOptions::new().max_connections(1000).connect(&db_url).await {
            Ok(p) => break p,
            Err(_) => {
                if retries == 0 { std::process::exit(1); }
                tokio::time::sleep(std::time::Duration::from_secs(3)).await;
                retries -= 1;
            }
        }
    };

    init_db(&pool).await;

    let redis_url = std::env::var("REDIS_URL").unwrap_or_else(|_| "redis://127.0.0.1:6379".to_string());
    let redis_client = redis::Client::open(redis_url).unwrap();
    let mut redis_retries = 5;
    let redis_conn = loop {
        match redis_client.get_multiplexed_async_connection().await {
            Ok(conn) => break conn,
            Err(_) => {
                if redis_retries == 0 { std::process::exit(1); }
                tokio::time::sleep(std::time::Duration::from_secs(2)).await;
                redis_retries -= 1;
            }
        }
    };

    let http_client = reqwest::Client::builder().pool_max_idle_per_host(500).build().unwrap();

    let state = AppState { db: pool, redis: redis_conn, http_client };
    let cors = CorsLayer::new().allow_origin(Any).allow_methods(Any).allow_headers(Any);

    let app = Router::new()
        .route("/api/status", get(api_status))
        .route("/api/user/login", post(auth::login))
        .route("/api/user/register", post(auth::register))
        .route("/api/user/self", get(user::get_self_info))
        .route("/v1/chat/completions", post(relay::chat_completions).route_layer(axum_middleware::from_fn_with_state(state.clone(), middleware::auth_middleware)))
        .fallback_service(ServeDir::new("web/dist"))
        .layer(cors)
        .with_state(state);

    let listener = TcpListener::bind("0.0.0.0:3000").await.unwrap();
    axum::serve(listener, app).await.unwrap();
}

async fn api_status() -> Json<Value> {
    Json(json!({"success": true, "message": "", "data": {"version": "v1.0.0", "system_name": "Nexus", "turnstile_site_key": "", "wechat_login": false, "github_oauth": true, "quota_per_unit": 500000, "HeaderNavModules": "{\"pricing\":{\"requireAuth\":false}}"}}))
}
