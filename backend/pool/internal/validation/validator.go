package validation

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"sharkpool/backend/pool/internal/accounting"
	"sharkpool/backend/pool/internal/cache"
	"sharkpool/backend/pool/internal/jobcache"
)

type Validator struct{
    cache *cache.Client
    acct accounting.AccountingAPI
    remote ShareRemoteValidator
    jobs *jobcache.JobCache
}

// NewValidator returns a permissive validator that allows nil jobcache for tests or specialized uses.
// For production use, prefer NewValidatorOrPanic to fail-fast on missing job cache.
func NewValidator(c *cache.Client, a accounting.AccountingAPI, grpc ShareRemoteValidator, j *jobcache.JobCache) *Validator {
    return &Validator{cache: c, acct: a, remote: grpc, jobs: j}
}

// (Constructor variants below.)

// NewStrictValidator returns a validator and error when a nil job cache is provided.
// This is recommended for production workflows where a nil job cache means the
// validator cannot verify job existence or difficulty.
func NewStrictValidator(c *cache.Client, a accounting.AccountingAPI, grpc ShareRemoteValidator, j *jobcache.JobCache) (*Validator, error) {
    if j == nil {
        return nil, errors.New("job cache must be configured for strict validation")
    }
    return &Validator{cache: c, acct: a, remote: grpc, jobs: j}, nil
}

// NewValidatorOrPanic is a convenience constructor intended for production
// servers that should not run without a configured job cache. It panics when
// a nil job cache is passed.
func NewValidatorOrPanic(c *cache.Client, a accounting.AccountingAPI, grpc ShareRemoteValidator, j *jobcache.JobCache) *Validator {
    if j == nil {
        panic("job cache must be configured for strict validation")
    }
    v, _ := NewStrictValidator(c, a, grpc, j)
    return v
}

// ValidateShare validates a miner's share. Steps:
// - check duplicate submission using redis
// - ensure job exists
// - record share via accounting engine
func (v *Validator) ValidateShare(ctx context.Context, submission *ShareSubmission) (bool, error) {
    if submission == nil {
        log.Printf("[VALIDATION] Received nil submission")
        return false, errors.New("nil submission")
    }
    log.Printf("[VALIDATION] Processing share: worker=%s, job_id=%s, nonce=%s, share_id=%s", submission.Worker, submission.JobId, submission.Nonce, submission.ShareId)
    // use redis to ensure dedup of the share id for a short TTL
    key := fmt.Sprintf("share:%s", submission.ShareId)
    ok, err := v.cache.SetIfNotExist(ctx, key, time.Minute)
    if err != nil {
        return false, fmt.Errorf("redis dedup: %w", err)
    }
    if !ok {
        // duplicate
        log.Printf("[VALIDATION] Duplicate share rejected: share_id=%s, worker=%s", submission.ShareId, submission.Worker)
        _, _ = v.acct.RecordShare(ctx, submission.Worker, submission.JobId, submission.Nonce, submission.ShareId, false)
        return false, nil
    }
    // ensure job exists
    if submission.JobId == "" {
        return false, errors.New("missing job id")
    }
    if v.jobs == nil {
        return false, errors.New("job cache not configured")
    }
    job, ok := v.jobs.GetJob(submission.JobId)
    if !ok {
        log.Printf("[VALIDATION] Unknown job: job_id=%s, worker=%s (available_jobs=%d)", submission.JobId, submission.Worker, len(v.jobs.ListJobs()))
        return false, errors.New("unknown job id")
    }
    log.Printf("[VALIDATION] Job found: job_id=%s, algo=%s, difficulty=%d", submission.JobId, job.Algorithm.String(), job.Difficulty)
    // compute the PoW hash and check against difficulty target
    headerSnippet := submission.Header
    if len(headerSnippet) > 16 { headerSnippet = headerSnippet[:16] }
    log.Printf("[VALIDATION] Computing hash: algo=%s, header=%s, nonce=%s", job.Algorithm.String(), headerSnippet, submission.Nonce)
    h, err := HashWithAlg(job.Algorithm, submission.Header, submission.Nonce)
    if err != nil {
        return false, err
    }
    log.Printf("[VALIDATION] Hash result: %x (meets_difficulty=%v)", h[:16], HashMeetsDifficulty(h, job.Difficulty))
    if !HashMeetsDifficulty(h, job.Difficulty) {
        // implicitly record rejected share
        _, _ = v.acct.RecordShare(ctx, submission.Worker, submission.JobId, submission.Nonce, submission.ShareId, false)
        return false, nil
    }
    accepted := true
    if v.remote != nil {
        // delegate to remote validator
        log.Printf("[VALIDATION] Delegating to remote validator: share_id=%s", submission.ShareId)
        valid, err := v.remote.ValidateShare(ctx, submission)
        if err != nil {
            return false, err
        }
        accepted = valid
        log.Printf("[VALIDATION] Remote validator result: valid=%v, share_id=%s", valid, submission.ShareId)
    }
    _, err = v.acct.RecordShare(ctx, submission.Worker, submission.JobId, submission.Nonce, submission.ShareId, accepted)
    if err != nil {
        return false, err
    }
    log.Printf("[VALIDATION] Share ACCEPTED: worker=%s, job_id=%s, nonce=%s, algo=%s", submission.Worker, submission.JobId, submission.Nonce, job.Algorithm.String())
    return accepted, nil
}

// Public types and interfaces used by the stratum server
type ShareSubmission struct{
    Worker string
    JobId string
    Nonce string
    ShareId string
    Difficulty int64
    Header string
    Algorithm jobcache.Algorithm
}

// ValidatorAPI is the interface implemented by Validators used by the stratum server.
type ValidatorAPI interface{
    ValidateShare(ctx context.Context, submission *ShareSubmission) (bool, error)
}

// AcceptAllValidator accepts everything for development.
type AcceptAllValidator struct {}

func (a *AcceptAllValidator) ValidateShare(ctx context.Context, submission *ShareSubmission) (bool, error) {
    select {
    case <-ctx.Done():
        return false, ctx.Err()
    case <-time.After(10 * time.Millisecond):
    }
    return true, nil
}
