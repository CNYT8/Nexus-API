use bcrypt::{hash, verify, BcryptError};

const BCRYPT_COST: u32 = 10;

pub fn hash_password(password: &str) -> Result<String, BcryptError> {
    hash(password, BCRYPT_COST)
}

pub fn verify_password(password: &str, hashed_password: &str) -> Result<bool, BcryptError> {
    verify(password, hashed_password)
}
