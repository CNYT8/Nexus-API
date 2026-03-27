use redis::{AsyncCommands, Script};
use axum::http::StatusCode;
use redis::aio::MultiplexedConnection;
use sqlx::MySqlPool;

pub async fn deduct_quota(
    db: &MySqlPool,
    redis_conn: &mut MultiplexedConnection,
    user_id: i32,
    token_id: i32,
    cost: i32,
    token_unlimited: i8,
) -> Result<bool, (StatusCode, String)> {
    let lua_script = Script::new(r#"
        local quota_key = KEYS[1]
        local cost = tonumber(ARGV[1])
        local current_quota = redis.call('GET', quota_key)
        if current_quota == false then return -1 end
        if tonumber(current_quota) >= cost then
            redis.call('DECRBY', quota_key, cost)
            return 1
        else
            return 0
        end
    "#);

    let result: i32 = lua_script
        .key(format!("user:quota:{}", user_id))
        .arg(cost)
        .invoke_async(redis_conn.clone())
        .await
        .map_err(|_| (StatusCode::INTERNAL_SERVER_ERROR, "".to_string()))?;

    if result == 1 {
        let _ = sqlx::query("UPDATE users SET quota = quota - ?, used_quota = used_quota + ? WHERE id = ? AND deleted_at IS NULL").bind(cost).bind(cost).bind(user_id).execute(db).await;
        if token_unlimited == 0 {
            let _ = sqlx::query("UPDATE tokens SET remain_quota = remain_quota - ?, used_quota = used_quota + ? WHERE id = ? AND deleted_at IS NULL").bind(cost).bind(cost).bind(token_id).execute(db).await;
        }
        return Ok(true);
    } else if result == 0 {
        return Err((StatusCode::PAYMENT_REQUIRED, "".to_string()));
    }

    let user_quota: Option<(i32,)> = sqlx::query_as("SELECT quota FROM users WHERE id = ? AND deleted_at IS NULL")
        .bind(user_id)
        .fetch_optional(db)
        .await
        .map_err(|_| (StatusCode::INTERNAL_SERVER_ERROR, "".to_string()))?
        .flatten();

    if let Some((q,)) = user_quota {
        if q >= cost {
            let _: () = redis_conn.set(format!("user:quota:{}", user_id), q - cost).await.unwrap_or(());
            let _ = sqlx::query("UPDATE users SET quota = quota - ?, used_quota = used_quota + ? WHERE id = ? AND deleted_at IS NULL").bind(cost).bind(cost).bind(user_id).execute(db).await;
            if token_unlimited == 0 {
                let _ = sqlx::query("UPDATE tokens SET remain_quota = remain_quota - ?, used_quota = used_quota + ? WHERE id = ? AND deleted_at IS NULL").bind(cost).bind(cost).bind(token_id).execute(db).await;
            }
            Ok(true)
        } else {
            Err((StatusCode::PAYMENT_REQUIRED, "".to_string()))
        }
    } else {
        Err((StatusCode::UNAUTHORIZED, "".to_string()))
    }
}
