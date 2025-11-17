use sharkpool_miner::utils::{format_hashrate, hash_meets_target, difficulty_to_target, target_to_difficulty};

#[test]
fn test_hashrate_formatting() {
    assert_eq!(format_hashrate(100.0), "100.00 H/s");
    assert_eq!(format_hashrate(1_500.0), "1.50 KH/s");
    assert_eq!(format_hashrate(2_500_000.0), "2.50 MH/s");
    assert_eq!(format_hashrate(3_500_000_000.0), "3.50 GH/s");
}

#[test]
fn test_difficulty_target_conversion() {
    // Test difficulty to target conversion
    let target1 = difficulty_to_target(1.0);
    assert_eq!(target1.len(), 64);
    
    let target2 = difficulty_to_target(2.0);
    assert!(target2 < target1, "Higher difficulty should result in lower target");
    
    // Test target to difficulty conversion
    let diff1 = target_to_difficulty(&target1);
    assert!((diff1 - 1.0).abs() < 0.1, "Should convert back to approximately 1.0");
}

#[test]
fn test_hash_meets_target() {
    // Very easy target (all F's = maximum value)
    let easy_target = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff";
    let zero_hash = vec![0u8; 32];
    assert!(hash_meets_target(&zero_hash, easy_target), "Zero hash should meet maximum target");
    
    // Very hard target (very low value)
    let hard_target = "0000000000000000000000000000000000000000000000000000000000000001";
    let high_hash = vec![0xffu8; 32];
    assert!(!hash_meets_target(&high_hash, hard_target), "High hash should not meet low target");
    
    // Test with 0x prefix
    assert!(hash_meets_target(&zero_hash, "0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"));
}

#[test]
fn test_hash_target_boundary() {
    // Create a hash that exactly matches the target
    let target = "0000000000000000000000000000000000000000000000000000000000001000";
    
    // Hash below target (should pass)
    let hash_below = vec![
        0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
        0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
        0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
        0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x0f, 0xff,
    ];
    assert!(hash_meets_target(&hash_below, target));
    
    // Hash above target (should fail)
    let hash_above = vec![
        0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
        0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
        0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
        0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x10, 0x01,
    ];
    assert!(!hash_meets_target(&hash_above, target));
}

#[cfg(test)]
mod stratum_tests {
    use sharkpool_miner::stratum::MiningJob;

    #[test]
    fn test_mining_job_creation() {
        let job = MiningJob {
            job_id: "test_job_1".to_string(),
            seed_hash: "a".repeat(64),
            header_hash: "b".repeat(64),
            clean_jobs: true,
        };

        assert_eq!(job.job_id, "test_job_1");
        assert_eq!(job.seed_hash.len(), 64);
        assert_eq!(job.header_hash.len(), 64);
        assert!(job.clean_jobs);
    }

    #[test]
    fn test_mining_job_clone() {
        let job1 = MiningJob {
            job_id: "job1".to_string(),
            seed_hash: "seed".to_string(),
            header_hash: "header".to_string(),
            clean_jobs: false,
        };

        let job2 = job1.clone();
        assert_eq!(job1.job_id, job2.job_id);
        assert_eq!(job1.seed_hash, job2.seed_hash);
        assert_eq!(job1.header_hash, job2.header_hash);
        assert_eq!(job1.clean_jobs, job2.clean_jobs);
    }
}

#[cfg(test)]
mod hashing_tests {
    use validator_rs::hashing::{fishhash, double_sha256, blake3_hash, argon2id_hash};

    #[test]
    fn test_fishhash_deterministic() {
        let header = "test_header";
        let nonce = "test_nonce";
        
        let hash1 = fishhash(header, nonce);
        let hash2 = fishhash(header, nonce);
        
        assert_eq!(hash1, hash2, "Fishhash should be deterministic");
        assert_eq!(hash1.len(), 32, "Fishhash should produce 32 bytes");
    }

    #[test]
    fn test_double_sha256_deterministic() {
        let header = "test_header";
        let nonce = "test_nonce";
        
        let hash1 = double_sha256(header, nonce);
        let hash2 = double_sha256(header, nonce);
        
        assert_eq!(hash1, hash2, "Double SHA256 should be deterministic");
        assert_eq!(hash1.len(), 32, "Double SHA256 should produce 32 bytes");
    }

    #[test]
    fn test_blake3_deterministic() {
        let header = "test_header";
        let nonce = "test_nonce";
        
        let hash1 = blake3_hash(header, nonce);
        let hash2 = blake3_hash(header, nonce);
        
        assert_eq!(hash1, hash2, "Blake3 should be deterministic");
        assert_eq!(hash1.len(), 32, "Blake3 should produce 32 bytes");
    }

    #[test]
    fn test_argon2id_deterministic() {
        let header = "test_header";
        let nonce = "test_nonce";
        
        let hash1 = argon2id_hash(header, nonce);
        let hash2 = argon2id_hash(header, nonce);
        
        assert_eq!(hash1, hash2, "Argon2id should be deterministic");
        assert_eq!(hash1.len(), 32, "Argon2id should produce 32 bytes");
    }

    #[test]
    fn test_different_nonces_different_hashes() {
        let header = "test_header";
        
        let hash1 = fishhash(header, "nonce1");
        let hash2 = fishhash(header, "nonce2");
        
        assert_ne!(hash1, hash2, "Different nonces should produce different hashes");
    }

    #[test]
    fn test_different_headers_different_hashes() {
        let nonce = "test_nonce";
        
        let hash1 = fishhash("header1", nonce);
        let hash2 = fishhash("header2", nonce);
        
        assert_ne!(hash1, hash2, "Different headers should produce different hashes");
    }
}

#[test]
fn test_stratum_header_fallback_keccak() {
    use tiny_keccak::{Keccak, Hasher};
    // raw header ascii string
    let raw_header = "test_header_ascii";
    // compute expected keccak (32 bytes) hex
    let mut hasher = Keccak::v256();
    hasher.update(raw_header.as_bytes());
    let mut expected = [0u8; 32];
    hasher.finalize(&mut expected);
    let expected_hex = hex::encode(expected);
    // simulate Stratum client logic: try hex decode first, otherwise keccak
    let header_vec = match hex::decode(raw_header) {
        Ok(h) => h,
        Err(_) => {
            let raw = raw_header.as_bytes().to_vec();
            if raw.len() != 32 {
                let mut hasher = Keccak::v256();
                hasher.update(&raw);
                let mut out = [0u8; 32];
                hasher.finalize(&mut out);
                out.to_vec()
            } else { raw }
        }
    };
    let header_hex = hex::encode(header_vec);
    assert_eq!(header_hex, expected_hex, "expected keccak hex equals header fallback result");
}
