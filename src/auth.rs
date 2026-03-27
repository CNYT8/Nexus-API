use axum::{Json, extract::State, http::StatusCode};
use axum_extra::extract::cookie::{Cookie, CookieJar};
use serde_json::{json, Value};
use jsonwebtoken::{encode, Header, EncodingKey};
use chrono::{Utc, Duration};
use crate::{AppState, models::User, crypto::verify_password};

#[derive(serde::Deserialize)]
pub struct LoginRequest {
    pub username: String,
    pub password: String,
}

#[derive(serde::Deserialize)]
pub struct RegisterRequest {
    pub username: String,
    pub password: String,
}

#[derive(serde::Serialize)]
struct Claims {
    id: i32,
    username: String,
    role: i32,
    exp: usize,
}

pub async fn login(
    State(state): State<AppState>,
    jar: CookieJar,
    Json(payload): Json<LoginRequest>,
) -> Result<(CookieJar, Json<Value>), (StatusCode, String)> {
    let user_res: Result<Option<User>, _> = sqlx::query_as(
        "SELECT id, username, password, display_name, role, status, quota, used_quota FROM users WHERE username = ? AND deleted_at IS NULL"
    )
    .bind(&payload.username)
    .fetch_optional(&state.db)
    .await;

    let user = match user_res {
        Ok(Some(u)) => u,
        _ => return Ok((jar, Json(json!({"success": false, "message": ""}))))
    };

    if !verify_password(&payload.password, &user.password).unwrap_or(false) {
        return Ok((jar, Json(json!({"success": false, "message": ""}))))
    }

    if user.status != 1 {
        return Ok((jar, Json(json!({"success": false, "message": ""}))))
    }

    let expiration = Utc::now().checked_add_signed(Duration::days(30)).unwrap().timestamp() as usize;
    let claims = Claims { id: user.id, username: user.username.clone(), role: user.role, exp: expiration };
    let secret = std::env::var("SESSION_SECRET").unwrap_or_else(|_| "nexus".to_string());
    
    let token = encode(&Header::default(), &claims, &EncodingKey::from_secret(secret.as_bytes()))
        .map_err(|_| (StatusCode::INTERNAL_SERVER_ERROR, "".to_string()))?;

    let cookie = Cookie::build(("session", token.clone())).path("/").http_only(true).build();

    let response_body = json!({
        "success": true,
        "message": "",
        "data": {
            "user": {
                "id": user.id,
                "username": user.username,
                "display_name": user.display_name,
                "role": user.role,
                "status": user.status,
            },
            "token": token
        }
    });

    Ok((jar.add(cookie), Json(response_body)))
}

pub async fn register(
    State(state): State<AppState>,
    Json(payload): Json<RegisterRequest>,
) -> Result<Json<Value>, (StatusCode, String)> {
    if payload.username.is_empty() || payload.password.len() < 6 {
        return Ok(Json(json!({"success": false, "message": ""})));
    }

    let exists: (i64,) = sqlx::query_as("SELECT count(*) FROM users WHERE username = ? AND deleted_at IS NULL")
        .bind(&payload.username)
        .fetch_one(&state.db)
        .await
        .unwrap_or((0,));

    if exists.0 > 0 {
        return Ok(Json(json!({"success": false, "message": ""})));
    }

    let hashed_pw = crate::crypto::hash_password(&payload.password)
        .map_err(|_| (StatusCode::INTERNAL_SERVER_ERROR, "".to_string()))?;

    let aff_code = format!("{:x}", Utc::now().timestamp_nanos_opt().unwrap_or(0));

    sqlx::query(
        "INSERT INTO users (username, password, display_name, role, status, quota, used_quota, `group`, aff_code) 
         VALUES (?, ?, ?, 1, 1, 0, 0, 'default', ?)"
    )
    .bind(&payload.username)
    .bind(hashed_pw)
    .bind(&payload.username)
    .bind(aff_code)
    .execute(&state.db)
    .await
    .map_err(|_| (StatusCode::INTERNAL_SERVER_ERROR, "".to_string()))?;

    Ok(Json(json!({"success": true, "message": ""})))
}
