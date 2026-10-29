package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync"
	"testing"
	"time"
)

// TestPingTimeoutFor: the startup Ping must have a deadline whether or not the configuration names
// one. Without it, an unreachable database blocks the assembly path for as long as the driver's own
// dial behaviour allows, and a missing dependency looks like a hung process.
func TestPingTimeoutFor(t *testing.T) {
	cases := []struct {
		name string
		cfg  *Config
		want time.Duration
	}{
		{"nil config falls back", nil, defaultPingTimeout},
		{"unset query timeout falls back", &Config{}, defaultPingTimeout},
		{"negative is not a deadline", &Config{QueryTimeout: -time.Second}, defaultPingTimeout},
		{"configured value wins", &Config{QueryTimeout: 3 * time.Second}, 3 * time.Second},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pingTimeoutFor(tc.cfg); got != tc.want {
				t.Fatalf("pingTimeoutFor = %v, want %v", got, tc.want)
			}
		})
	}
}

// blockingPingDriver models a database that accepts the connection and never answers the ping —
// a network black hole, or a pool saturated by another client. It is the case the deadline exists
// for: a driver that refuses the connection immediately would fail fast without one.
type blockingPingDriver struct{}

func (blockingPingDriver) Open(string) (driver.Conn, error) { return blockingPingConn{}, nil }

type blockingPingConn struct{}

func (blockingPingConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unsupported") }
func (blockingPingConn) Close() error                        { return nil }
func (blockingPingConn) Begin() (driver.Tx, error)           { return nil, errors.New("unsupported") }

// Ping waits for the context to expire, which is what a silent database looks like to database/sql.
func (blockingPingConn) Ping(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

var registerBlockingDriver sync.Once

// TestPingStartup_HonoursTheDeadline is the wiring test: the deadline must reach PingContext, not
// merely exist as a helper return value. Without it this test blocks until the test binary's
// timeout.
func TestPingStartup_HonoursTheDeadline(t *testing.T) {
	registerBlockingDriver.Do(func() { sql.Register("ares-blockingping", blockingPingDriver{}) })

	db, err := sql.Open("ares-blockingping", "")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	start := time.Now()
	err = pingStartup(db, 40*time.Millisecond)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a database that never answers must not be reported as reachable")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("the startup ping ignored its deadline: took %v", elapsed)
	}
}
