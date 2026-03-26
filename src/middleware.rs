use axum::{
    extract::{Request, State},
    middleware::Next,
    response::IntoResponse,
    http::{StatusCode, header::AUTHORIZATION},
};
use crate::AppState;

pub async fn auth_middleware(
    State(_state): State<AppState>,
    req: Request,
    next: Next,
) -> Result<impl IntoResponse, (StatusCode, String)> {
    let auth_header = req.headers().get(AUTHORIZATION)
        .and_then(|h| h.to_str().ok())
        .unwrap_or_default();

    if !auth_header.starts_with("Bearer sk-") {
        return Err((StatusCode::UNAUTHORIZED, "Invalid token".to_string()));
    }

    Ok(next.run(req).await)
}
