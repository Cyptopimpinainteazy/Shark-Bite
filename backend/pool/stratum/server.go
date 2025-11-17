package stratum

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"sharkpool/backend/pool/internal/auth"
	"sharkpool/backend/pool/internal/cache"
	"sharkpool/backend/pool/internal/jobcache"
	"sharkpool/backend/pool/internal/metrics"
	"sharkpool/backend/pool/internal/validation"
)

// ServerOptions configures the Stratum server behavior
type ServerOptions struct {
    TLSCertFile string
    TLSKeyFile  string
    MaxConnections int
    RatePerWindow int
    RateWindow time.Duration
}

type Server struct {
    addr string
    ln net.Listener
    validator validation.ValidatorAPI
    authEngine *auth.AuthEngine
    redisClient *cache.Client
    jobs *jobcache.JobCache

    conns map[string]*ConnState
    connsMu sync.RWMutex
    ctx context.Context
    cancel context.CancelFunc
    wg sync.WaitGroup

    tlsConfig *tls.Config
    tlsEnabled bool
    maxConns int
    sem chan struct{}
    rateWindow time.Duration
    ratePerWindow int
    rateMu sync.Mutex
    rateMap map[string]int
}

type ConnState struct {
    conn net.Conn
    r *bufio.Reader
    w *bufio.Writer
    remote string
    mu sync.Mutex
    authorized bool
    worker string
    clientSoftware string // miner user-agent string
    notifySeedFirst bool   // true if miner expects seed first ordering
}

// sendNotification sends a notification (no id) to the miner.
func (cs *ConnState) sendNotification(method string, params interface{}) error {
    nr := struct {
        Method string `json:"method"`
        Params interface{} `json:"params"`
    }{Method: method, Params: params}
    cs.mu.Lock(); defer cs.mu.Unlock()
    if err := json.NewEncoder(cs.w).Encode(nr); err != nil { return err }
    return cs.w.Flush()
}

type RPCRequest struct {
    ID json.RawMessage `json:"id,omitempty"`
    Method string `json:"method"`
    Params json.RawMessage `json:"params,omitempty"`
}

type RPCResponse struct {
    ID json.RawMessage `json:"id,omitempty"`
    Result interface{} `json:"result,omitempty"`
    Error interface{} `json:"error,omitempty"`
}

func NewServer(addr string, validator validation.ValidatorAPI, authEngine *auth.AuthEngine, redisClient *cache.Client, jobs *jobcache.JobCache, opts *ServerOptions) *Server {
    ctx, cancel := context.WithCancel(context.Background())
    s := &Server{
        addr: addr,
        validator: validator,
        authEngine: authEngine,
        redisClient: redisClient,
        jobs: jobs,
        conns: make(map[string]*ConnState),
        ctx: ctx,
        cancel: cancel,
        rateMap: make(map[string]int),
    }
    if opts != nil {
        if opts.MaxConnections > 0 { s.maxConns = opts.MaxConnections; s.sem = make(chan struct{}, s.maxConns) }
        if opts.RatePerWindow > 0 { s.ratePerWindow = opts.RatePerWindow; s.rateWindow = opts.RateWindow; if s.rateWindow==0 { s.rateWindow = time.Second } }
        if opts.TLSCertFile != "" && opts.TLSKeyFile != "" {
            cert, err := tls.LoadX509KeyPair(opts.TLSCertFile, opts.TLSKeyFile)
            if err == nil { s.tlsConfig = &tls.Config{Certificates: []tls.Certificate{cert}}; s.tlsEnabled = true }
        }
    }
    return s
}

func (s *Server) Start() error {
    var ln net.Listener
    var err error
    if s.tlsEnabled && s.tlsConfig != nil {
        ln, err = tls.Listen("tcp", s.addr, s.tlsConfig)
    } else {
        ln, err = net.Listen("tcp", s.addr)
    }
    if err != nil { return fmt.Errorf("listen: %w", err) }
    s.ln = ln
    s.wg.Add(1)
    go s.acceptLoop()
    return nil
}

func (s *Server) Stop() {
    s.cancel()
    if s.ln != nil { _ = s.ln.Close() }
    s.connsMu.Lock()
    for _, c := range s.conns { _ = c.conn.Close() }
    s.connsMu.Unlock()
    s.wg.Wait()
}

func (s *Server) acceptLoop() {
    defer s.wg.Done()
    for {
        conn, err := s.ln.Accept()
        if err != nil {
            select {
            case <-s.ctx.Done(): return
            default:
                log.Printf("accept error: %v", err)
                time.Sleep(200*time.Millisecond)
                continue
            }
        }
        if s.maxConns > 0 {
            select { case s.sem <- struct{}{}: default: _ = conn.Close(); continue }
        }
        cs := &ConnState{ conn: conn, r: bufio.NewReader(conn), w: bufio.NewWriter(conn), remote: conn.RemoteAddr().String() }
        s.connsMu.Lock(); s.conns[cs.remote] = cs; s.connsMu.Unlock(); metrics.CurrentConnections.WithLabelValues("stratum").Inc()
        s.wg.Add(1)
        go func() { defer s.wg.Done(); s.handleConn(cs) }()
    }
}

func (cs *ConnState) sendResponse(resp *RPCResponse) error {
    cs.mu.Lock(); defer cs.mu.Unlock()
    if err := json.NewEncoder(cs.w).Encode(resp); err!=nil { return err }
    return cs.w.Flush()
}

func (s *Server) handleConn(cs *ConnState) {
    connStart := time.Now()
    log.Printf("[FISHHASH-POOL] New connection from %s", cs.remote)
    defer func() {
        _ = cs.conn.Close(); s.connsMu.Lock(); delete(s.conns, cs.remote); s.connsMu.Unlock(); metrics.CurrentConnections.WithLabelValues("stratum").Dec(); if s.maxConns>0 { <-s.sem }
        log.Printf("[FISHHASH-POOL] Disconnected: %s (worker=%s, uptime=%s)", cs.remote, cs.worker, time.Since(connStart))
    }()
    for {
        _ = cs.conn.SetReadDeadline(time.Now().Add(5*time.Minute))
        line, err := cs.r.ReadBytes('\n')
        if err != nil { if err == io.EOF { return }; return }
        msg := strings.TrimSpace(string(line))
        if msg == "" { continue }
        var req RPCRequest
        if err := json.Unmarshal([]byte(msg), &req); err != nil { log.Printf("[WARN] Invalid JSON from %s: %s", cs.remote, msg); _ = cs.sendResponse(&RPCResponse{ID:nil, Error:"invalid json"}); continue }
        ip := cs.conn.RemoteAddr().(*net.TCPAddr).IP.String()
        if !s.allowRequest(context.Background(), ip) { metrics.RateLimited.WithLabelValues("stratum_ip").Inc(); _ = cs.sendResponse(&RPCResponse{ID:req.ID, Error:"rate limit exceeded"}); continue }
        s.handleRequest(cs, &req)
    }
}

func (s *Server) allowRequest(ctx context.Context, ip string) bool {
    if s.ratePerWindow <= 0 { return true }
    if s.redisClient != nil {
        val, err := s.redisClient.IncrWithExpire(ctx, fmt.Sprintf("ratelimit:ip:%s", ip), s.rateWindow)
        if err == nil { return val <= int64(s.ratePerWindow) }
    }
    s.rateMu.Lock(); defer s.rateMu.Unlock()
    if _, ok := s.rateMap[ip]; !ok {
        s.rateMap[ip] = 1; return true
    }
    if s.rateMap[ip] >= s.ratePerWindow { return false }
    s.rateMap[ip]++; return true
}

func (s *Server) handleRequest(cs *ConnState, req *RPCRequest) {
    switch req.Method {
    case "mining.subscribe": s.handleSubscribe(cs, req)
    case "mining.authorize": s.handleAuthorize(cs, req)
    case "mining.submit": s.handleSubmit(cs, req)
    default: s.logUnsupported(cs, req); _ = cs.sendResponse(&RPCResponse{ID:req.ID, Error: fmt.Sprintf("unsupported method %s", req.Method)})
    }
}

// log unknown method attempts for easier debugging
func (s *Server) logUnsupported(cs *ConnState, req *RPCRequest) {
    log.Printf("[WARN] Unsupported method from %s: %s params=%s", cs.remote, req.Method, string(req.Params))
}

// isHex validates whether s is a hex-encoded string (no 0x prefix expected)
func isHex(s string) bool {
    if s == "" { return false }
    _, err := hex.DecodeString(s)
    return err == nil
}

func isHexLen(s string, length int) bool {
    if s == "" { return false }
    if len(s) != length { return false }
    return isHex(s)
}

// supportsAlgo checks if the client software supports a given algorithm
func supportsAlgo(clientSoftware, algorithm string) bool {
    if algorithm == "fishhash" {
        // Assume all miners support fishhash for now
        return true
    }
    return false
}

func (s *Server) handleSubscribe(cs *ConnState, req *RPCRequest) {
    // Ethereum-style stratum response
    // extranonce should be 2-8 hex characters (1-4 bytes)
    extranonce := "00"
    extranonce2Size := 4
    // Ethereum-style subscribe: first element is array of subscription arrays
    // for example: [["mining.notify","subscriptionId"]], extranonce, extranonce2Size
    subId := fmt.Sprintf("sub_%d", time.Now().UnixNano())
    res := []interface{}{
        []interface{}{[]interface{}{"mining.notify", subId}},
        extranonce,
        extranonce2Size,
    }
    _ = cs.sendResponse(&RPCResponse{ID:req.ID, Result: res})
    log.Printf("[FISHHASH-POOL] Sent subscribe response to %s: subId=%s extranonce=%s extranonce2Size=%d", cs.remote, subId, extranonce, extranonce2Size)

    // Detect client software and preferred notify ordering from the subscribe params
    // params typically: [client, optional...]
    var params []string
    if err := json.Unmarshal(req.Params, &params); err == nil && len(params) > 0 {
        cs.clientSoftware = params[0]
        // if we detect lolMiner, set seed-first ordering (lolMiner historically uses job_id, seed_hash, header_hash)
            if strings.Contains(strings.ToLower(cs.clientSoftware), "lolminer") || strings.Contains(strings.ToLower(cs.clientSoftware), "herominers") {
            cs.notifySeedFirst = true
            log.Printf("[FISHHASH-POOL] Detected miner %s at %s, using seed-first notify ordering", cs.clientSoftware, cs.remote)
                metrics.MinerClientDetected.WithLabelValues("lolminer").Inc()
        } else {
            cs.notifySeedFirst = false
            log.Printf("[FISHHASH-POOL] Miner %s at %s, using header-first notify ordering", cs.clientSoftware, cs.remote)
                metrics.MinerClientDetected.WithLabelValues("other").Inc()
        }
    } else {
        // default: header-first
        cs.notifySeedFirst = false
    }
    log.Printf("[FISHHASH-POOL] Detected miner: %s from %s (supports_fishhash=%v)", cs.clientSoftware, cs.remote, supportsAlgo(cs.clientSoftware, "fishhash"))
    
    // Send initial job after subscription
    if s.jobs != nil {
        jobs := s.jobs.ListJobs()
        if len(jobs) > 0 {
            j := jobs[len(jobs)-1]
            go func() {
                time.Sleep(100 * time.Millisecond)
                s.sendEthereumJob(cs, j)
            }()
        }
    }
}

// sendEthereumJob sends Ethereum-style mining job
func (s *Server) sendEthereumJob(cs *ConnState, job jobcache.JobInfo) {
    // Ethereum stratum job format:
    // ["job_id", "seed_hash", "header_hash", true]
    // For Fishhash, we use simplified format compatible with Ethash miners
    
    // Use precomputed seed/header hash from job if available; otherwise generate deterministic ones
    seedHash := job.SeedHash
    if seedHash == "" {
        seedHash = fmt.Sprintf("%064x", time.Now().Unix())
    }
    // Use the raw header string where possible to ensure miner and validator compute hash over the same bytes.
    headerParam := job.Header
    if headerParam == "" {
        // Fallback to headerHash (hex) if header is missing
        headerParam = job.HeaderHash
    }
    
    // Ordering selection: some miners (lolMiner) expect [job_id, seed_hash, header_hash, clean_jobs]
    // Use per-connection preference to send the appropriate ordering.
    var jobParams []interface{}
    if cs.notifySeedFirst {
        jobParams = []interface{}{job.JobID, seedHash, headerParam, true}
    } else {
        jobParams = []interface{}{job.JobID, headerParam, seedHash, true}
    }
    _ = cs.sendNotification("mining.notify", jobParams)
    log.Printf("[ETH] Sent mining.notify job=%s seed=%s header=%s to %s", job.JobID, seedHash, headerParam, cs.remote)
    log.Printf("[FISHHASH-POOL] Using %s notify ordering for %s", func() string { if cs.notifySeedFirst { return "seed-first" } else { return "header-first" } }(), cs.clientSoftware)
    metrics.EthereumNotifySent.WithLabelValues(job.Algorithm.String()).Inc()
    
    // Set target difficulty in full 256-bit hex string (big-endian)
    // Target = MaxTarget / Difficulty (canonical 256-bit target)
    targetInt := validation.DifficultyToTarget(job.Difficulty)
    target := fmt.Sprintf("%064x", targetInt)
    _ = cs.sendNotification("mining.set_target", []interface{}{target})
    log.Printf("[ETH] Sent mining.set_target target=%s job=%s to %s", target, job.JobID, cs.remote)
    // Use safe substrings for logging in case header/seed are shorter than expected
    seedSnippet := seedHash
    if len(seedSnippet) > 16 { seedSnippet = seedSnippet[:16] }
    headerSnippet := headerParam
    if len(headerSnippet) > 16 { headerSnippet = headerSnippet[:16] }
    targetSnippet := target
    if len(targetSnippet) > 16 { targetSnippet = targetSnippet[:16] }
    log.Printf("[FISHHASH-POOL] Broadcast job to %s: job_id=%s, seed=%s, header=%s, difficulty=%d, target=%s", cs.remote, job.JobID, seedSnippet, headerSnippet, job.Difficulty, targetSnippet)
    metrics.EthereumTargetSet.WithLabelValues(job.Algorithm.String()).Inc()
}

func (s *Server) handleAuthorize(cs *ConnState, req *RPCRequest) {
    var params []string
    if err := json.Unmarshal(req.Params, &params); err != nil { _ = cs.sendResponse(&RPCResponse{ID:req.ID, Result:false, Error:"invalid params"}); return }
    if len(params) < 1 { _ = cs.sendResponse(&RPCResponse{ID:req.ID, Result:false, Error:"missing username"}); return }
    worker := params[0]; password := ""
    if len(params) >= 2 { password = params[1] }
    // Strip worker suffix (wallet.workername -> wallet)
    username := worker
    if idx := strings.Index(worker, "."); idx > 0 {
        username = worker[:idx]
    }
    if s.authEngine != nil {
        ok, err := s.authEngine.Authenticate(context.Background(), username, password)
        if err != nil { _ = cs.sendResponse(&RPCResponse{ID:req.ID, Result:false, Error:"auth error"}); return }
        if !ok { _ = cs.sendResponse(&RPCResponse{ID:req.ID, Result:false}); return }
    }
    log.Printf("[AUTH] Authorized worker=%s (username=%s) from %s", worker, username, cs.remote)
    cs.authorized = true
    cs.worker = worker
    metrics.AuthAttempts.WithLabelValues("success").Inc()
    _ = cs.sendResponse(&RPCResponse{ID:req.ID, Result:true})
}

func (s *Server) handleSubmit(cs *ConnState, req *RPCRequest) {
    log.Printf("[SUBMIT] Received submit from %s: %s", cs.remote, string(req.Params))
    if !cs.authorized { _ = cs.sendResponse(&RPCResponse{ID:req.ID, Result:false, Error:"unauthorized"}); return }
    var rawParams []interface{}
    if err := json.Unmarshal(req.Params, &rawParams); err != nil { _ = cs.sendResponse(&RPCResponse{ID:req.ID, Result:false, Error:"invalid params"}); return }

    // Normalize to []string
    sparams := make([]string, 0, len(rawParams))
    for _, p := range rawParams {
        switch v := p.(type) {
        case string:
            sparams = append(sparams, v)
        case float64:
            // some miners might send numbers; stringify
            sparams = append(sparams, fmt.Sprintf("%v", uint64(v)))
        default:
            // ignore other types
        }
    }

    // Ethereum stratum submit format variants: [user, job_id, nonce, header_hash, mix_hash]
    // Some miners provide different ordering; we will attempt to detect nonce & header via heuristics
    sub := &validation.ShareSubmission{Worker: cs.worker}
    if len(sparams) >= 1 { sub.Worker = sparams[0] }
    if len(sparams) >= 2 { sub.JobId = sparams[1] }

    // Detect nonce: scan remaining params; prefer 8-16 hex chars or numeric
    nonceFound := false
    for i := 2; i < len(sparams); i++ {
        candidate := sparams[i]
        candidateTrim := strings.TrimPrefix(candidate, "0x")
        if isHex(candidateTrim) {
            // Accept nonce if it's small-ish
            if len(candidateTrim) <= 128 && len(candidateTrim) >= 2 {
                sub.Nonce = candidateTrim
                nonceFound = true
                // If a header param is included we may want to map it
                break
            }
        }
        // otherwise accept as nonce too
        if !nonceFound {
            sub.Nonce = candidate
            nonceFound = true
            break
        }
    }
    if !nonceFound && len(sparams) >= 3 {
        sub.Nonce = sparams[2]
    }

    // Attempt to find header and seed in sparams
    headerIdx, seedIdx := -1, -1
    for i := 2; i < len(sparams); i++ {
        cand := strings.TrimPrefix(sparams[i], "0x")
        if isHexLen(cand, 64) {
            if headerIdx == -1 {
                headerIdx = i
            } else if seedIdx == -1 {
                seedIdx = i
            }
        }
    }
    if headerIdx != -1 {
        // if header param was found, set it
        sub.Header = strings.TrimPrefix(sparams[headerIdx], "0x")
    }
    // If submit includes both header and seed and notify ordering is inconsistent, detect format
    submitFormat := "unknown"
    if headerIdx != -1 && seedIdx != -1 {
        if seedIdx < headerIdx {
            submitFormat = "seed_first"
        } else {
            submitFormat = "header_first"
        }
    } else if headerIdx != -1 {
        // only header found
        submitFormat = "header_first"
    } else if seedIdx != -1 {
        submitFormat = "seed_first"
    }
    if submitFormat != "unknown" {
        metrics.SubmitFormatDetected.WithLabelValues(submitFormat).Inc()
        log.Printf("[SUBMIT] Detected submit format=%s for conn=%s user=%s", submitFormat, cs.remote, cs.clientSoftware)
    }
    // params[3] would be header_hash (we have it in job)
    // params[4] would be mix_hash (optional for Fishhash)
    sub.ShareId = fmt.Sprintf("share_%s_%d", cs.remote, time.Now().UnixNano())
    if s.jobs != nil {
        job, ok := s.jobs.GetJob(sub.JobId)
        // try to heuristically find job id if initial param wasn't correct
        if !ok {
            // look for job_... token in params
            for i := 1; i < len(sparams); i++ {
                if strings.HasPrefix(sparams[i], "job_") {
                    sub.JobId = sparams[i]
                    break
                }
            }
            if sub.JobId != "" {
                job, ok = s.jobs.GetJob(sub.JobId)
            }
        }
        // still not found: try find by provided header/seed hash in params
        if !ok {
            for i := 2; i < len(sparams); i++ {
                cand := strings.TrimPrefix(sparams[i], "0x")
                if isHexLen(cand, 64) {
                    if j, found := s.jobs.FindJobByHash(cand); found {
                        job = j
                        sub.JobId = j.JobID
                        ok = true
                        break
                    }
                }
            }
        }
        if !ok {
            _ = cs.sendResponse(&RPCResponse{ID:req.ID, Result:false, Error:"invalid job"})
            metrics.ShareRejected.WithLabelValues("unknown_job").Inc()
            return
        }
        // attach difficulty, header and algorithm into the submission for remote validator
        sub.Difficulty = job.Difficulty
        sub.Header = job.Header
        sub.Algorithm = job.Algorithm
        log.Printf("[FISHHASH-POOL] Validating share from %s: job_id=%s, nonce=%s, algo=%s", cs.worker, sub.JobId, sub.Nonce, job.Algorithm.String())
        h, _ := validation.HashWithAlg(job.Algorithm, job.Header, sub.Nonce)
        log.Printf("[FISHHASH-POOL] Hash computed: %x (meets_difficulty=%v)", h[:16], validation.HashMeetsDifficulty(h, job.Difficulty))
        if !validation.HashMeetsDifficulty(h, job.Difficulty) {
            // fallback: if obvious nonce positions were wrong, try different positions as nonce
            metrics.SubmitFallbackAttempted.WithLabelValues("nonce_positions").Inc()
            fallbackSuccess := false
            for i := 2; i < len(sparams); i++ {
                candidate := strings.TrimPrefix(sparams[i], "0x")
                if candidate == "" || candidate == sub.Nonce { continue }
                if !isHex(candidate) { continue }
                fh, _ := validation.HashWithAlg(job.Algorithm, job.Header, candidate)
                if validation.HashMeetsDifficulty(fh, job.Difficulty) {
                    // Accept as valid nonce
                    sub.Nonce = candidate
                    fallbackSuccess = true
                    metrics.SubmitFallbackSuccessful.WithLabelValues("nonce_positions").Inc()
                    break
                }
            }
            if !fallbackSuccess {
                _ = cs.sendResponse(&RPCResponse{ID:req.ID, Result:false})
                metrics.ShareRejected.WithLabelValues("difficulty").Inc()
                return
            }
        }
    }
    log.Printf("[SUBMIT] Validating share %s for job=%s nonce=%s from worker=%s", sub.ShareId, sub.JobId, sub.Nonce, sub.Worker)
    ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
    defer cancel()
    valid, verr := s.validator.ValidateShare(ctx, sub)
    if verr != nil {
        log.Printf("[FISHHASH-POOL] Share REJECTED from %s: job_id=%s, nonce=%s, reason=validation_error", cs.worker, sub.JobId, sub.Nonce)
        _ = cs.sendResponse(&RPCResponse{ID:req.ID, Result:false, Error:"validation error"}); return
    }
    if valid {
        log.Printf("[FISHHASH-POOL] Share ACCEPTED from %s: job_id=%s, nonce=%s", cs.worker, sub.JobId, sub.Nonce)
        _ = cs.sendResponse(&RPCResponse{ID:req.ID, Result:true}); metrics.ShareAccepted.WithLabelValues("stratum").Inc(); return
    } else {
        log.Printf("[FISHHASH-POOL] Share REJECTED from %s: job_id=%s, nonce=%s, reason=validator_rejected", cs.worker, sub.JobId, sub.Nonce)
    }
    // If validator rejected, we may also attempt alternate nonce positions as another fallback
    metrics.SubmitFallbackAttempted.WithLabelValues("validator_reject").Inc()
    for i := 2; i < len(sparams); i++ {
        candidate := strings.TrimPrefix(sparams[i], "0x")
        if candidate == "" || candidate == sub.Nonce { continue }
        if !isHex(candidate) { continue }
        sub.Nonce = candidate
        ctx2, cancel2 := context.WithTimeout(s.ctx, 3*time.Second)
        valid2, verr2 := s.validator.ValidateShare(ctx2, sub)
        cancel2()
        if verr2 == nil && valid2 {
            metrics.SubmitFallbackSuccessful.WithLabelValues("validator_reject").Inc()
            _ = cs.sendResponse(&RPCResponse{ID:req.ID, Result:true})
            metrics.ShareAccepted.WithLabelValues("stratum").Inc()
            return
        }
    }
    _ = cs.sendResponse(&RPCResponse{ID:req.ID, Result:false}); metrics.ShareRejected.WithLabelValues("stratum").Inc()
    return
    _ = cs.sendResponse(&RPCResponse{ID:req.ID, Result:false}); metrics.ShareRejected.WithLabelValues("stratum").Inc()
}

// BroadcastNewJob notifies all connected miners about a new job.
func (s *Server) BroadcastNewJob(job jobcache.JobInfo) {
    s.connsMu.RLock(); defer s.connsMu.RUnlock()
    for _, cs := range s.conns {
        go func(c *ConnState) {
            s.sendEthereumJob(c, job)
        }(cs)
    }
}
