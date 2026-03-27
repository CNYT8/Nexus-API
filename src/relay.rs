use axum::{
    extract::{State, Extension, Request},
    response::IntoResponse,
    http::{StatusCode, HeaderMap, HeaderValue},
    body::Body,
};
use serde_json::Value;
use crate::{AppState, middleware::TokenContext, billing::deduct_quota, channel::select_channel_for_model};

pub async fn chat_completions(
    State(state): State<AppState>,
    Extension(token_ctx): Extension<TokenContext>,
    req: Request,
) -> Result<impl IntoResponse, (StatusCode, String)> {
    let mut redis_conn = state.redis.clone();
    
    let body_bytes = axum::body::to_bytes(req.into_body(), usize::MAX).await
        .map_err(|_| (StatusCode::BAD_REQUEST, "".to_string()))?;
    
    let payload: Value = serde_json::from_slice(&body_bytes)
        .map_err(|_| (StatusCode::BAD_REQUEST, "".to_string()))?;

    let model_name = payload["model"].as_str().unwrap_or("").to_string();
    if model_name.is_empty() {
        return Err((StatusCode::BAD_REQUEST, "".to_string()));
    }

    let is_stream = payload["stream"].as_bool().unwrap_or(false);

    let channel = select_channel_for_model(&state.db, &model_name, "default").await?;

    let base_url = channel.base_url.unwrap_or_else(|| "https://api.openai.com".to_string());
    let upstream_url = format!("{}/v1/chat/completions", base_url.trim_end_matches('/'));

    let mut headers = HeaderMap::new();
    headers.insert("Content-Type", HeaderValue::from_static("application/json"));
    
    let active_key = channel.key.lines().next().unwrap_or("").trim();
    if let Ok(auth_val) = HeaderValue::from_str(&format!("Bearer {}", active_key)) {
        headers.insert("Authorization", auth_val);
    }

    let upstream_res = state.http_client
        .post(&upstream_url)
        .headers(headers)
        .json(&payload)
        .send()
        .await
        .map_err(|_| (StatusCode::BAD_GATEWAY, "".to_string()))?;

    let upstream_status = upstream_res.status();

    if !upstream_status.is_success() {
        let err_body = upstream_res.text().await.unwrap_or_default();
        return Err((upstream_status, err_body));
    }

    let estimated_cost = 1000; 

    if token_ctx.unlimited_quota == 0 {
        if let Err(_) = deduct_quota(&state.db, &mut redis_conn, token_ctx.user_id, token_ctx.token_id, estimated_cost, token_ctx.unlimited_quota).await {
            return Err((StatusCode::PAYMENT_REQUIRED, "".to_string()));
        }
    }

    let _ = sqlx::query(
        "INSERT INTO logs (user_id, created_at, type, content, quota, prompt_tokens, completion_tokens, use_time, is_stream, model_name, token_name, channel_id) 
         VALUES (?, ?, 1, '', ?, 0, 0, 0, ?, ?, '', ?)"
    )
    .bind(token_ctx.user_id)
    .bind(chrono::Utc::now().timestamp())
    .bind(estimated_cost)
    .bind(if is_stream { 1 } else { 0 })
    .bind(&model_name)
    .bind(channel.id)
    .execute(&state.db)
    .await;

    let mut response_builder = axum::response::Response::builder()
        .status(upstream_status);

    for (k, v) in upstream_res.headers() {
        if k != "transfer-encoding" && k != "content-length" {
            response_builder = response_builder.header(k, v);
        }
    }

    let axum_body = Body::from_stream(upstream_res.bytes_stream());
    let response = response_builder.body(axum_body).unwrap();

    Ok(response)
}
