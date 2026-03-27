use sqlx::{MySqlPool, FromRow};
use axum::http::StatusCode;

#[derive(Debug, FromRow)]
pub struct Channel {
    pub id: i32,
    pub type_: i32,
    pub key: String,
    pub base_url: Option<String>,
    pub models: String,
    pub weight: i32,
}

pub async fn select_channel_for_model(
    db: &MySqlPool,
    model_name: &str,
    group: &str,
) -> Result<Channel, (StatusCode, String)> {
    let query = r#"
        SELECT id, type, `key`, base_url, models, weight 
        FROM channels 
        WHERE status = 1 
          AND deleted_at IS NULL 
          AND FIND_IN_SET(?, models) > 0
          AND `group` = ?
        ORDER BY weight DESC 
        LIMIT 1
    "#;

    let channel: Option<Channel> = sqlx::query_as(query)
        .bind(model_name)
        .bind(group)
        .fetch_optional(db)
        .await
        .map_err(|_| (StatusCode::INTERNAL_SERVER_ERROR, "".to_string()))?;

    channel.ok_or_else(|| (StatusCode::SERVICE_UNAVAILABLE, "".to_string()))
}
