use axum::{
    extract::{Request, State},
    middleware::Next,
    response::IntoResponse,
    http::{StatusCode, header::AUTHORIZATION},
};
use crate::AppState;

#[derive(Clone)]
pub struct TokenContext {
    pub token_id: i32,
    pub user_id: i32,
    pub remain_quota: i32,
    pub unlimited_quota: i8,
}

pub async fn auth_middleware(
    State(state): State<AppState>,
    mut req: Request,
    next: Next,
) -> Result<impl IntoResponse, (StatusCode, String)> {
    let auth_header = req.headers().get(AUTHORIZATION)
        .and_then(|h| h.to_str().ok())
        .unwrap_or_default();

    if !auth_header.starts_with("Bearer sk-") {
        return Err((StatusCode::UNAUTHORIZED, "".to_string()));
    }

    let token_key = &auth_header[7..];

    let token_record: Option<(i32, i32, i32, i8, i32, i64)> = sqlx::query_as(
        "SELECT id, user_id, remain_quota, unlimited_quota, status, expired_time FROM tokens WHERE `key` = ? AND deleted_at IS NULL"
    )
    .bind(token_key)
    .fetch_optional(&state.db)
    .await
    .map_err(|_| (StatusCode::INTERNAL_SERVER_ERROR, "".to_string()))?;

    if let Some((token_id, user_id, remain_quota, unlimited_quota, status, expired_time)) = token_record {
        if status != 1 {
            return Err((StatusCode::FORBIDDEN, "".to_string()));
        }

        let now = chrono::Utc::now().timestamp();
        if expired_time != -1 && expired_time < now {
            return Err((StatusCode::FORBIDDEN, "".to_string()));
        }

        if unlimited_quota == 0 && remain_quota <= 0 {
            return Err((StatusCode::PAYMENT_REQUIRED, "".to_string()));
        }

        req.extensions_mut().insert(TokenContext {
            token_id,
            user_id,
            remain_quota,
            unlimited_quota,
        });

        Ok(next.run(req).await)
    } else {
        Err((StatusCode::UNAUTHORIZED, "".to_string()))
    }
}
