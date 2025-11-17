package validation

import (
	"encoding/hex"
	"sharkpool/backend/pool/internal/jobcache"
	"testing"
)

// Test vectors computed from validator-rs example/compute_vectors
const (
    testHeader = "test_header"
    testNonce  = "test_nonce"
    expectedFishHashRaw = "167545b2bfa8eb48dd1dcfc8d989260441fa03a51b20d543f43f15a00f7024d6"
    expectedHeaderHashHex = "bbba5f91c5c364ddf70124bd718c9310236a416d649fd9434c05421e6de2778f"
    expectedFishHashHeaderHashBytes = "7e1a8f2b8de5264c3c15b55807981d74de150d42c5520271658eda0eaa42022d"
)

func TestFishhashRawParity(t *testing.T) {
    // Compute using HashWithAlg with raw header and nonce
    h, err := HashWithAlg(jobcache.AlgoFishhash, testHeader, testNonce)
    if err != nil { t.Fatalf("HashWithAlg failed: %v", err) }
    got := hex.EncodeToString(h)
    if got != expectedFishHashRaw {
        t.Fatalf("Fishhash raw mismatch: expected=%s got=%s", expectedFishHashRaw, got)
    }
}

func TestFishhashHeaderHashHexParity(t *testing.T) {
    // Compute using header hash hex as provided by the pool
    h, err := HashWithAlg(jobcache.AlgoFishhash, expectedHeaderHashHex, testNonce)
    if err != nil { t.Fatalf("HashWithAlg failed: %v", err) }
    got := hex.EncodeToString(h)
    if got != expectedFishHashHeaderHashBytes {
        t.Fatalf("Fishhash header-hash-bytes mismatch: expected=%s got=%s", expectedFishHashHeaderHashBytes, got)
    }
}
