package stratum

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"sharkpool/backend/pool/internal/auth"
	"sharkpool/backend/pool/internal/db"
	"sharkpool/backend/pool/internal/jobcache"
	"sharkpool/backend/pool/internal/validation"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"golang.org/x/crypto/bcrypt"
)

func TestSubscribeAndAuthorize(t *testing.T) {
    s := NewServer("127.0.0.1:0", &validation.AcceptAllValidator{}, nil, nil, nil, nil)
    if err := s.Start(); err != nil {
        t.Fatalf("start server: %v", err)
    }
    defer s.Stop()
    addr := s.ln.Addr().String()
    conn, err := net.Dial("tcp", addr)
    if err != nil {
        t.Fatalf("dial server: %v", err)
    }
    defer conn.Close()
    r := bufio.NewReader(conn)
    // Subscribe
    req := map[string]interface{}{"id": 1, "method": "mining.subscribe", "params": []interface{}{"shark/1.0.0"}}
    b, _ := json.Marshal(req)
    fmt.Fprintf(conn, "%s\n", string(b))
    line, _ := r.ReadString('\n')
    if !strings.Contains(line, "sub_") {
        t.Fatalf("unexpected subscribe response: %s", line)
    }
    // Authorize
    req2 := map[string]interface{}{"id": 2, "method": "mining.authorize", "params": []interface{}{"worker1","x"}}
    b2, _ := json.Marshal(req2)
    fmt.Fprintf(conn, "%s\n", string(b2))
    line, _ = r.ReadString('\n')
    if !strings.Contains(line, "true") {
        t.Fatalf("unexpected authorize response: %s", line)
    }
}

func TestSubmitAccepted(t *testing.T) {
    s := NewServer("127.0.0.1:0", &validation.AcceptAllValidator{}, nil, nil, nil, nil)
    if err := s.Start(); err != nil {
        t.Fatalf("start server: %v", err)
    }
    defer s.Stop()
    addr := s.ln.Addr().String()
    conn, err := net.Dial("tcp", addr)
    if err != nil {
        t.Fatalf("dial server: %v", err)
    }
    defer conn.Close()
    r := bufio.NewReader(conn)
    // Authorize
    req2 := map[string]interface{}{"id": 1, "method": "mining.authorize", "params": []interface{}{"worker1","x"}}
    b2, _ := json.Marshal(req2)
    fmt.Fprintf(conn, "%s\n", string(b2))
    _, _ = r.ReadString('\n')
    // Submit
    req3 := map[string]interface{}{"id": 2, "method": "mining.submit", "params": []interface{}{"worker1", "job1", "0001"}}
    b3, _ := json.Marshal(req3)
    fmt.Fprintf(conn, "%s\n", string(b3))
    line, _ := r.ReadString('\n')
    if !strings.Contains(line, "true") {
        t.Fatalf("unexpected submit response: %s", line)
    }
}

func TestSubmitInvalidJSON(t *testing.T) {
    s := NewServer("127.0.0.1:0", &validation.AcceptAllValidator{}, nil, nil, nil, nil)
    if err := s.Start(); err != nil {
        t.Fatalf("start server: %v", err)
    }
    defer s.Stop()
    addr := s.ln.Addr().String()
    conn, err := net.Dial("tcp", addr)
    if err != nil {
        t.Fatalf("dial server: %v", err)
    }
    defer conn.Close()
    r := bufio.NewReader(conn)
    // Send invalid json
    fmt.Fprintf(conn, "%s\n", "{ not a json }")
    line, _ := r.ReadString('\n')
    if !strings.Contains(line, "invalid json") {
        t.Fatalf("unexpected response for invalid json: %s", line)
    }
}

func TestRateLimitPerIP(t *testing.T) {
    // Account for the initial mining.authorize counting as one message
    opts := &ServerOptions{MaxConnections: 0, RatePerWindow: 3, RateWindow: time.Second}
    s := NewServer("127.0.0.1:0", &validation.AcceptAllValidator{}, nil, nil, nil, opts)
    if err := s.Start(); err != nil {
        t.Fatalf("start server: %v", err)
    }
    defer s.Stop()
    addr := s.ln.Addr().String()
    conn, err := net.Dial("tcp", addr)
    if err != nil {
        t.Fatalf("dial server: %v", err)
    }
    defer conn.Close()
    r := bufio.NewReader(conn)
    // Authorize first to allow submit
    req2 := map[string]interface{}{"id": 1, "method": "mining.authorize", "params": []interface{}{"worker1","x"}}
    b2, _ := json.Marshal(req2)
    fmt.Fprintf(conn, "%s\n", string(b2))
    _, _ = r.ReadString('\n')
    // Submit repeatedly to exceed rate
    for i := 0; i < 4; i++ {
        req3 := map[string]interface{}{"id": i+2, "method": "mining.submit", "params": []interface{}{"worker1", "job1", fmt.Sprintf("%04d", i)}}
        b3, _ := json.Marshal(req3)
        fmt.Fprintf(conn, "%s\n", string(b3))
        line, _ := r.ReadString('\n')
        if i < 2 {
            if !strings.Contains(line, "true") {
                t.Fatalf("unexpected submit response: %s", line)
            }
        } else {
            if !strings.Contains(line, "rate limit exceeded") {
                t.Fatalf("expected rate limited, got: %s", line)
            }
        }
    }
}

func TestMaxConnections(t *testing.T) {
    opts := &ServerOptions{MaxConnections: 1}
    s := NewServer("127.0.0.1:0", &validation.AcceptAllValidator{}, nil, nil, nil, opts)
    if err := s.Start(); err != nil {
        t.Fatalf("start server: %v", err)
    }
    defer s.Stop()
    addr := s.ln.Addr().String()
    c1, err := net.Dial("tcp", addr)
    if err != nil {
        t.Fatalf("dial 1: %v", err)
    }
    defer c1.Close()
    c2, err := net.Dial("tcp", addr)
    if err == nil {
        // second connection may be accepted or closed by server due to limit. We assert that the connection
        // is not usable: either write returns an error or a subsequent read yields EOF.
        _, werr := c2.Write([]byte("{}\n"))
        if werr == nil {
            // attempt to read: if read succeeds, then the server didn't limit the connection — fail the test
            buf := make([]byte, 1)
            c2.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
            if _, rerr := c2.Read(buf); rerr == nil {
                t.Fatalf("expected second connection to be denied or closed")
            }
        }
    }
}

func TestBroadcastNewJob(t *testing.T) {
    jc := jobcache.NewCache()
    s := NewServer("127.0.0.1:0", &validation.AcceptAllValidator{}, nil, nil, jc, nil)
    if err := s.Start(); err != nil { t.Fatalf("start server: %v", err) }
    defer s.Stop()
    addr := s.ln.Addr().String()
    conn, err := net.Dial("tcp", addr)
    if err != nil { t.Fatalf("dial server: %v", err) }
    defer conn.Close()
    r := bufio.NewReader(conn)
    // subscribe
    req := map[string]interface{}{"id": 1, "method": "mining.subscribe", "params": []interface{}{"shark/1.0.0"}}
    b, _ := json.Marshal(req)
    fmt.Fprintf(conn, "%s\n", string(b))
    _, _ = r.ReadString('\n')
    // broadcast job
    s.BroadcastNewJob(jobcache.JobInfo{JobID: "bjob", Difficulty: 2})
    // notification should be received
    line, err := r.ReadString('\n')
    if err != nil { t.Fatalf("read notification: %v", err) }
    if !strings.Contains(line, "mining.notify") || !strings.Contains(line, "bjob") {
        t.Fatalf("unexpected notify payload: %s", line)
    }
}

func TestSubscribeDetectLolMiner(t *testing.T) {
    jc := jobcache.NewCache()
    jc.AddJob("joblol", 1, jobcache.AlgoFishhash)
    job, _ := jc.GetJob("joblol")
    s := NewServer("127.0.0.1:0", &validation.AcceptAllValidator{}, nil, nil, jc, nil)
    if err := s.Start(); err != nil { t.Fatalf("start server: %v", err) }
    defer s.Stop()
    addr := s.ln.Addr().String()
    conn, err := net.Dial("tcp", addr)
    if err != nil { t.Fatalf("dial server: %v", err) }
    defer conn.Close()
    r := bufio.NewReader(conn)
    // Subscribe with lolMiner user-agent
    req := map[string]interface{}{"id": 1, "method": "mining.subscribe", "params": []interface{}{"lolMiner/1.88"}}
    b, _ := json.Marshal(req)
    fmt.Fprintf(conn, "%s\n", string(b))
    // read subscribe response
    _, _ = r.ReadString('\n')
    // read notify
    line, _ := r.ReadString('\n')
    var notify map[string]interface{}
    if err := json.Unmarshal([]byte(line), &notify); err != nil { t.Fatalf("unmarshal notify: %v", err) }
    params, ok := notify["params"].([]interface{})
    if !ok || len(params) < 3 {
        t.Fatalf("unexpected notify payload: %s", line)
    }
    // since it's lolMiner, we expect seed first ordering (job_id, seed, header, clean)
    seed := params[1].(string)
    if seed != job.SeedHash {
        t.Fatalf("expected seed hash %s got %s", job.SeedHash, seed)
    }
}

func TestSetTarget256Bit(t *testing.T) {
    jc := jobcache.NewCache()
    jc.AddJob("jobt", 1, jobcache.AlgoFishhash)
    s := NewServer("127.0.0.1:0", &validation.AcceptAllValidator{}, nil, nil, jc, nil)
    if err := s.Start(); err != nil { t.Fatalf("start server: %v", err) }
    defer s.Stop()
    addr := s.ln.Addr().String()
    conn, err := net.Dial("tcp", addr)
    if err != nil { t.Fatalf("dial server: %v", err) }
    defer conn.Close()
    r := bufio.NewReader(conn)
    // subscribe
    req := map[string]interface{}{"id": 1, "method": "mining.subscribe", "params": []interface{}{"shark/1.0.0"}}
    b, _ := json.Marshal(req)
    fmt.Fprintf(conn, "%s\n", string(b))
    _, _ = r.ReadString('\n')
    // wait for notify and set_target
    // We expect mining.notify and mining.set_target to be sent
    line, _ := r.ReadString('\n')
    if !strings.Contains(line, "mining.notify") {
        t.Fatalf("expected notify, got: %s", line)
    }
    // set_target will be sent separately; read next notification
    line2, _ := r.ReadString('\n')
    if !strings.Contains(line2, "mining.set_target") { t.Fatalf("expected set_target, got: %s", line2) }
    var notif map[string]interface{}
    if err := json.Unmarshal([]byte(line2), &notif); err != nil { t.Fatalf("unmarshal set_target: %v", err) }
    params, ok := notif["params"].([]interface{})
    if !ok || len(params) < 1 { t.Fatalf("unexpected set_target: %s", line2) }
    target, ok := params[0].(string)
    if !ok { t.Fatalf("target is not a string: %v", params[0]) }
    if len(target) != 64 { t.Fatalf("expected 64 hex chars target, got len=%d value=%s", len(target), target) }
}

// Ensure mining.notify uses the raw job.Header if present (not header_hash)
func TestNotifyUsesRawHeader(t *testing.T) {
    jc := jobcache.NewCache()
    jc.AddJob("jobheader", 1, jobcache.AlgoFishhash)
    job, _ := jc.GetJob("jobheader")
    // ensure header is present
    if job.Header == "" { t.Fatalf("expected job.Header to be set for fishhash jobs") }
    s := NewServer("127.0.0.1:0", &validation.AcceptAllValidator{}, nil, nil, jc, nil)
    if err := s.Start(); err != nil { t.Fatalf("start server: %v", err) }
    defer s.Stop()
    addr := s.ln.Addr().String()
    conn, err := net.Dial("tcp", addr)
    if err != nil { t.Fatalf("dial server: %v", err) }
    defer conn.Close()
    r := bufio.NewReader(conn)
    // subscribe and wait for notify
    req := map[string]interface{}{"id": 1, "method": "mining.subscribe", "params": []interface{}{"shark/1.0.0"}}
    b, _ := json.Marshal(req)
    fmt.Fprintf(conn, "%s\n", string(b))
    _, _ = r.ReadString('\n')
    // expect mining.notify with header equal to job.Header
    line, _ := r.ReadString('\n')
    if !strings.Contains(line, "mining.notify") { t.Fatalf("expected notify payload, got: %s", line) }
    var notify map[string]interface{}
    if err := json.Unmarshal([]byte(line), &notify); err != nil { t.Fatalf("unmarshal notify: %v", err) }
    params, ok := notify["params"].([]interface{})
    if !ok || len(params) < 3 { t.Fatalf("unexpected notify payload: %s", line) }
    // depending on ordering, find header param (job.Header should be present as header param)
    headerParam := ""
    if csHeader, ok := params[1].(string); ok && csHeader == job.Header { headerParam = params[1].(string) }
    if headerParam == "" {
        if h2, ok := params[2].(string); ok && h2 == job.Header { headerParam = h2 }
    }
    if headerParam != job.Header { t.Fatalf("expected notify to contain raw header=%s, got=%v (payload=%s)", job.Header, headerParam, line) }
}

// Ensure a submit with raw header is accepted (when job.Header is provided)
func TestSubmitAcceptsRawHeader(t *testing.T) {
    jc := jobcache.NewCache()
    jc.AddJob("jobraw", 1, jobcache.AlgoFishhash)
    job, _ := jc.GetJob("jobraw")
    s := NewServer("127.0.0.1:0", &validation.AcceptAllValidator{}, nil, nil, jc, nil)
    if err := s.Start(); err != nil { t.Fatalf("start server: %v", err) }
    defer s.Stop()
    addr := s.ln.Addr().String()
    conn, err := net.Dial("tcp", addr)
    if err != nil { t.Fatalf("dial server: %v", err) }
    defer conn.Close()
    r := bufio.NewReader(conn)
    // subscribe
    req := map[string]interface{}{"id": 1, "method": "mining.subscribe", "params": []interface{}{"ethminer/0.18"}}
    b, _ := json.Marshal(req)
    fmt.Fprintf(conn, "%s\n", string(b))
    _, _ = r.ReadString('\n')
    // authorize
    reqAuth := map[string]interface{}{"id": 2, "method": "mining.authorize", "params": []interface{}{"worker1","x"}}
    ba, _ := json.Marshal(reqAuth)
    fmt.Fprintf(conn, "%s\n", string(ba))
    _, _ = r.ReadString('\n')
    // submit using raw header (not headerHash)
    reqSubmit := map[string]interface{}{"id": 3, "method": "mining.submit", "params": []interface{}{"worker1", job.JobID, "0001", job.Header, job.SeedHash}}
    bs, _ := json.Marshal(reqSubmit)
    fmt.Fprintf(conn, "%s\n", string(bs))
    line, _ := r.ReadString('\n')
    if !strings.Contains(line, "true") { t.Fatalf("expected submit accepted with raw header, got: %s", line) }
}

func TestSubmitFormatsHeaderAndSeedFirst(t *testing.T) {
    jc := jobcache.NewCache()
    jc.AddJob("jobfmt", 1, jobcache.AlgoFishhash)
    job, _ := jc.GetJob("jobfmt")
    s := NewServer("127.0.0.1:0", &validation.AcceptAllValidator{}, nil, nil, jc, nil)
    if err := s.Start(); err != nil { t.Fatalf("start server: %v", err) }
    defer s.Stop()
    addr := s.ln.Addr().String()
    // header-first case (default)
    conn1, err := net.Dial("tcp", addr)
    if err != nil { t.Fatalf("dial server: %v", err) }
    defer conn1.Close()
    r1 := bufio.NewReader(conn1)
    // subscribe with default agent
    req := map[string]interface{}{"id": 1, "method": "mining.subscribe", "params": []interface{}{"ethminer/0.18"}}
    b, _ := json.Marshal(req)
    fmt.Fprintf(conn1, "%s\n", string(b))
    _, _ = r1.ReadString('\n')
    // authorize
    reqAuth := map[string]interface{}{"id": 2, "method": "mining.authorize", "params": []interface{}{"worker1","x"}}
    ba, _ := json.Marshal(reqAuth)
    fmt.Fprintf(conn1, "%s\n", string(ba))
    _, _ = r1.ReadString('\n')
    // submit header first: [worker, job, nonce, header, seed]
    reqSubmit := map[string]interface{}{"id": 3, "method": "mining.submit", "params": []interface{}{"worker1", job.JobID, "0001", job.HeaderHash, job.SeedHash}}
    bs, _ := json.Marshal(reqSubmit)
    fmt.Fprintf(conn1, "%s\n", string(bs))
    line, _ := r1.ReadString('\n')
    if !strings.Contains(line, "true") {
        t.Fatalf("expected submit accepted for header-first, got: %s", line)
    }

    // seed-first case (lolMiner)
    conn2, err := net.Dial("tcp", addr)
    if err != nil { t.Fatalf("dial server: %v", err) }
    defer conn2.Close()
    r2 := bufio.NewReader(conn2)
    // subscribe as lolMiner
    req2 := map[string]interface{}{"id": 1, "method": "mining.subscribe", "params": []interface{}{"lolMiner/1.88"}}
    b2, _ := json.Marshal(req2)
    fmt.Fprintf(conn2, "%s\n", string(b2))
    _, _ = r2.ReadString('\n')
    // authorize
    reqAuth2 := map[string]interface{}{"id": 2, "method": "mining.authorize", "params": []interface{}{"worker1","x"}}
    ba2, _ := json.Marshal(reqAuth2)
    fmt.Fprintf(conn2, "%s\n", string(ba2))
    _, _ = r2.ReadString('\n')
    // submit seed-first: [worker, job, nonce, seed, header]
    reqSubmit2 := map[string]interface{}{"id": 4, "method": "mining.submit", "params": []interface{}{"worker1", job.JobID, "0001", job.SeedHash, job.HeaderHash}}
    bs2, _ := json.Marshal(reqSubmit2)
    fmt.Fprintf(conn2, "%s\n", string(bs2))
    line2, _ := r2.ReadString('\n')
    if !strings.Contains(line2, "true") {
        t.Fatalf("expected submit accepted for seed-first, got: %s", line2)
    }
}

func TestSubmitFallbackJobIdPositions(t *testing.T) {
    jc := jobcache.NewCache()
    jc.AddJob("jobfb", 1, jobcache.AlgoFishhash)
    job, _ := jc.GetJob("jobfb")
    s := NewServer("127.0.0.1:0", &validation.AcceptAllValidator{}, nil, nil, jc, nil)
    if err := s.Start(); err != nil { t.Fatalf("start server: %v", err) }
    defer s.Stop()
    addr := s.ln.Addr().String()
    conn, err := net.Dial("tcp", addr)
    if err != nil { t.Fatalf("dial server: %v", err) }
    defer conn.Close()
    r := bufio.NewReader(conn)
    // subscribe and authorize
    req := map[string]interface{}{"id": 1, "method": "mining.subscribe", "params": []interface{}{"ethminer/0.18"}}
    b, _ := json.Marshal(req)
    fmt.Fprintf(conn, "%s\n", string(b))
    _, _ = r.ReadString('\n')
    reqAuth := map[string]interface{}{"id": 2, "method": "mining.authorize", "params": []interface{}{"worker1","x"}}
    ba, _ := json.Marshal(reqAuth)
    fmt.Fprintf(conn, "%s\n", string(ba))
    _, _ = r.ReadString('\n')
    // Now craft a submit where job id is in a non-standard position (i.e., index 3)
    reqSubmit := map[string]interface{}{"id": 3, "method": "mining.submit", "params": []interface{}{"worker1", "0001", job.SeedHash, job.JobID}}
    bs, _ := json.Marshal(reqSubmit)
    fmt.Fprintf(conn, "%s\n", string(bs))
    line, _ := r.ReadString('\n')
    if !strings.Contains(line, "true") {
        t.Fatalf("expected submit accepted via fallback job id detection, got: %s", line)
    }
}

func TestAuthorizeWithDB(t *testing.T) {
    // set up sqlmock and auth engine
    dbsql, mock, err := sqlmock.New()
    if err != nil { t.Fatalf("sqlmock init: %v", err) }
    defer dbsql.Close()
    d := db.NewMockDB(dbsql)
    // bcrypt hash the password
    hashed, _ := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.DefaultCost)
    mock.ExpectQuery(`SELECT password_hash, active FROM miners`).WithArgs("worker1").WillReturnRows(sqlmock.NewRows([]string{"password_hash","active"}).AddRow(string(hashed), true))
    authEngine := auth.NewAuthEngine(d)

    s := NewServer("127.0.0.1:0", &validation.AcceptAllValidator{}, authEngine, nil, nil, &ServerOptions{MaxConnections: 10})
    if err := s.Start(); err != nil { t.Fatalf("start server: %v", err) }
    defer s.Stop()

    addr := s.ln.Addr().String()
    conn, err := net.Dial("tcp", addr)
    if err != nil { t.Fatalf("dial: %v", err) }
    defer conn.Close()
    r := bufio.NewReader(conn)
    // send authorize with password
    req := map[string]interface{}{"id": 1, "method": "mining.authorize", "params": []interface{}{"worker1","secret"}}
    b, _ := json.Marshal(req)
    fmt.Fprintf(conn, "%s\n", string(b))
    line, _ := r.ReadString('\n')
    if !strings.Contains(line, "true") {
        t.Fatalf("expected auth success, got: %s", line)
    }
}
