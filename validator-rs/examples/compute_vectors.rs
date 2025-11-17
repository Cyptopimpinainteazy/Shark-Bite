use validator_rs::hashing::{fishhash, fishhash_bytes};
use keccak_hash::keccak;
use hex;

fn main() {
    let header = "test_header";
    let nonce = "test_nonce";
    let hex_hash = hex::encode(fishhash(header, nonce));
    println!("FISHHASH_RAW: {}", hex_hash);

    // compute header keccak (header hash)
    let out = keccak(header.as_bytes());
    let header_hash_hex = hex::encode(out.as_bytes());
    println!("HEADER_HASH_HEX: {}", header_hash_hex);

    let bytes_hash_hex = hex::encode(fishhash_bytes(&out.as_bytes(), nonce.as_bytes()));
    println!("FISHHASH_HEADER_HASH_BYTES: {}", bytes_hash_hex);
}
