use validator_rs::hashing::{fishhash, fishhash_bytes};
use keccak_hash::keccak;
use hex;

#[test]
fn test_fishhash_raw_parity() {
    let header = "test_header";
    let nonce = "test_nonce";
    let expected = "167545b2bfa8eb48dd1dcfc8d989260441fa03a51b20d543f43f15a00f7024d6";
    let got = hex::encode(fishhash(header, nonce));
    assert_eq!(got, expected);
}

#[test]
fn test_fishhash_header_hash_bytes_parity() {
    let header = "test_header";
    let nonce = "test_nonce";
    // compute header hash keccak
    let out = keccak(header.as_bytes());
    let header_hash_hex = hex::encode(out.as_bytes());
    assert_eq!(header_hash_hex, "bbba5f91c5c364ddf70124bd718c9310236a416d649fd9434c05421e6de2778f");
    let got = hex::encode(fishhash_bytes(out.as_bytes(), nonce.as_bytes()));
    assert_eq!(got, "7e1a8f2b8de5264c3c15b55807981d74de150d42c5520271658eda0eaa42022d");
}
