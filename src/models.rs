use serde::{Deserialize, Serialize};
use sqlx::FromRow;

#[derive(Debug, Serialize, Deserialize, FromRow)]
pub struct User {
    pub id: i32,
    pub username: String,
    pub password: String,
    pub display_name: Option<String>,
    pub role: i32, 
    pub status: i32, 
    pub quota: i32, 
    pub used_quota: i32, 
}

#[derive(Debug, Serialize, Deserialize, FromRow)]
pub struct Log {
    pub id: i64,
    pub user_id: i32,
    // 终极修复：使用宏将 Rust 内部的 type_ 强行映射到 MySQL 的 type 字段，防止读写崩溃！
    #[sqlx(rename = "type")]
    pub type_: i32,
    pub model_name: String,
    pub prompt_tokens: i32,
    pub completion_tokens: i32,
    pub quota: i32,
    pub use_time: i32,
}
