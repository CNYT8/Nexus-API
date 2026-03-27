use axum::{Json, extract::State, http::StatusCode, http::header::AUTHORIZATION, extract::Request};
use serde_json::{json, Value};
use jsonwebtoken::{decode, DecodingKey, Validation};
use crate::{AppState, models::User};

#[derive(serde::Deserialize, serde::Serialize)]
struct Claims {
    id: i32,
    username: String,
    role: i32,
    exp: usize,
}

pub async fn get_self_info(
    State(state): State<AppState>,
    req: Request,
) -> Result<Json<Value>, (StatusCode, String)> {
    let auth_header = req.headers().get(AUTHORIZATION)
        .and_then(|h| h.to_str().ok())
        .unwrap_or_default();

    if !auth_header.starts_with("Bearer ") {
        return Err((StatusCode::UNAUTHORIZED, "".to_string()));
    }
    let token = &auth_header[7..];

    let secret = std::env::var("SESSION_SECRET").unwrap_or_else(|_| "nexus".to_string());
    
    let token_data = match decode::<Claims>(token, &DecodingKey::from_secret(secret.as_bytes()), &Validation::default()) {
        Ok(c) => c,
        Err(_) => return Err((StatusCode::UNAUTHORIZED, "".to_string())),
    };

    let user: Option<User> = sqlx::query_as(
        "SELECT id, username, password, display_name, role, status, quota, used_quota FROM users WHERE id = ? AND deleted_at IS NULL"
    )
    .bind(token_data.claims.id)
    .fetch_optional(&state.db)
    .await
    .map_err(|_| (StatusCode::INTERNAL_SERVER_ERROR, "".to_string()))?;

    if let Some(u) = user {
        Ok(Json(json!({
            "success": true,
            "message": "",
            "data": {
                "id": u.id,
                "username": u.username,
                "display_name": u.display_name,
                "role": u.role,
                "status": u.status,
                "quota": u.quota,
                "used_quota": u.used_quota
            }
        })))
    } else {
        Err((StatusCode::NOT_FOUND, "".to_string()))
    }
}
