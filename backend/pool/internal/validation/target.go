package validation

import (
	"crypto/sha256"
	"encoding/hex"
	"math/big"
	"sharkpool/backend/pool/internal/jobcache"

	blake3 "github.com/zeebo/blake3"
	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/sha3"
)

// MaxTarget is the maximum target used for 256-bit SHA2-based PoW
var MaxTarget *big.Int

func init() {
    // max target is 2^256 - 1
    MaxTarget = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
}

// DoubleSHA256Hex computes double SHA256 of header and nonce provided as hex or raw string
func DoubleSHA256Hex(headerHex, nonceHex string) ([]byte, error) {
    h1, err := hex.DecodeString(headerHex)
    if err != nil {
        // if not valid hex, use raw
        h1 = []byte(headerHex)
    }
    n1, err2 := hex.DecodeString(nonceHex)
    if err2 != nil {
        n1 = []byte(nonceHex)
    }
    hasher := sha256.New()
    hasher.Write(h1)
    hasher.Write(n1)
    first := hasher.Sum(nil)
    hasher.Reset()
    hasher.Write(first)
    return hasher.Sum(nil), nil
}

// Blake3Hex computes the Blake3 hash of header+nonce
func Blake3Hex(headerHex, nonceHex string) ([]byte, error) {
    // For Fishhash, treat header and nonce as raw ASCII bytes (consistent with Rust fishhash)
    h1 := []byte(headerHex)
    n1 := []byte(nonceHex)
    hasher := blake3.New()
    hasher.Write(h1)
    hasher.Write(n1)
    return hasher.Sum(nil), nil
}

// Argon2Hex computes a deterministic Argon2id digest of header+nonce; result is 32 bytes
// Uses the same parameters as Rust validator for consistency:
// - Salt derived deterministically from header using SHA256
// - m_cost: 4096 KiB (4 MB)
// - t_cost: 3 iterations
// - p_cost: 1 lane (single-threaded for determinism)
func Argon2Hex(headerHex, nonceHex string) ([]byte, error) {
    h1, err := hex.DecodeString(headerHex)
    if err != nil { h1 = []byte(headerHex) }
    n1, err2 := hex.DecodeString(nonceHex)
    if err2 != nil { n1 = []byte(nonceHex) }
    
    // Deterministic salt generation from header (matches Rust implementation)
    saltHasher := sha256.New()
    saltHasher.Write([]byte("SharkPool-Argon2-Salt-v1:"))
    saltHasher.Write(h1)
    saltHash := saltHasher.Sum(nil)
    salt := saltHash[:16] // Use first 16 bytes as salt
    
    // Password is header+nonce concatenated
    password := append(h1, n1...)
    
    // Argon2id parameters matching Rust: time=3, memory=4096 KiB, threads=1, keyLen=32
    dk := argon2.IDKey(password, salt, 3, 4096, 1, 32)
    return dk, nil
}

// FishhashHex computes the Keccak-256 hash of header and nonce formatted as an 80-byte buffer
func FishhashHex(headerHex, nonceHex string) ([]byte, error) {
    // Follow Rust semantics: treat header and nonce as raw ascii if not hex, concat them,
    // then copy up to 80 bytes into a fixed 80-byte buffer.
    h1, err := hex.DecodeString(headerHex)
    if err != nil { h1 = []byte(headerHex) }
    n1, err2 := hex.DecodeString(nonceHex)
    if err2 != nil { n1 = []byte(nonceHex) }

    // Concatenate header and nonce, then copy up to 80 bytes
    tmp := append([]byte{}, h1...)
    tmp = append(tmp, n1...)
    input := make([]byte, 80)
    copy_len := len(tmp)
    if copy_len > 80 { copy_len = 80 }
    copy(input[:copy_len], tmp[:copy_len])
    // Remaining 40 bytes are zeros (already initialized)
    
    hasher := sha3.NewLegacyKeccak256()
    hasher.Write(input)
    return hasher.Sum(nil), nil
}

// HashWithAlg chooses which hash algorithm to use
func HashWithAlg(algo jobcache.Algorithm, headerHex, nonceHex string) ([]byte, error) {
    switch algo {
    case jobcache.AlgoSha256d:
        return DoubleSHA256Hex(headerHex, nonceHex)
    case jobcache.AlgoBlake3:
        return Blake3Hex(headerHex, nonceHex)
    case jobcache.AlgoArgon2:
        return Argon2Hex(headerHex, nonceHex)
    case jobcache.AlgoFishhash:
        return FishhashHex(headerHex, nonceHex)
    default:
        return DoubleSHA256Hex(headerHex, nonceHex)
    }
}

// HashBigInt returns the big.Int representing the provided hash as big-endian
func HashBigInt(h []byte) *big.Int {
    // treat hash as big-endian
    bi := new(big.Int).SetBytes(h)
    return bi
}

// DifficultyToTarget converts a difficulty value into a numerical target.
// For Bitcoin-like model: target = MaxTarget / difficulty
func DifficultyToTarget(difficulty int64) *big.Int {
    if difficulty <= 0 { return MaxTarget }
    t := new(big.Int).Div(MaxTarget, big.NewInt(difficulty))
    return t
}

// HashMeetsDifficulty checks if the hash meets difficulty threshold (i.e., hash <= target)
func HashMeetsDifficulty(hash []byte, difficulty int64) bool {
    hi := HashBigInt(hash)
    target := DifficultyToTarget(difficulty)
    return hi.Cmp(target) <= 0
}
