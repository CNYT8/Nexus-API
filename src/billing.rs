use redis::{AsyncCommands, Script};
use axum::http::StatusCode;
use redis::aio::MultiplexedConnection;

pub async fn deduct_quota(
    redis_conn: &mut MultiplexedConnection,
    user_id: i32,
    cost: i32,
) -> Result<bool, (StatusCode, String)> {
    let lua_script = Script::new(r#"
        local quota_key = KEYS[1]
        local cost = tonumber(ARGV[1])
        local current_quota = redis.call('GET', quota_key)
        if current_quota == false then
            return -1
        end
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
        .invoke_async(redis_conn)
        .await
        .map_err(|_| (StatusCode::INTERNAL_SERVER_ERROR, "Redis internal error".to_string()))?;

    match result {
        1 => Ok(true),
        0 => Err((StatusCode::PAYMENT_REQUIRED, "Insufficient quota".to_string())),
        -1 => Ok(false),
        _ => Err((StatusCode::INTERNAL_SERVER_ERROR, "Unknown billing state".to_string())),
    }
}
