package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Version is the gateway's own version, reported on /health and in logs.
const Version = "0.1.0"

// config holds everything the gateway reads from the environment. Every value
// has a safe default; nothing here is a secret (device tokens live in SQLite
// and the admin token in a file on the server).
type config struct {
	Listen  string // phone-facing TLS address, e.g. :443
	Admin   string // admin API address, always loopback
	Cert    string // server certificate (chain) for the phone
	Key     string // server private key
	DBPath  string // SQLite file
	OpenURL string // opencode server base URL, e.g. http://127.0.0.1:4096

	Mock     bool // fake replies, never calls opencode
	Project  string // opencode project directory to point the server at
	Model    string // optional "provider/model" override
	Agent    string // default agent for a new phone conversation
	System   string // extra system prompt for every message
	RetentionDays int

	DailyLimit  int // requests per device per UTC day (0 = unlimited)
	Timeout     time.Duration // one chat request's budget
	OpenTimeout time.Duration // opencode health/prompt budget
	AdminToken  string

	// Tool policy. opencode is a coding agent: its build agent can run bash and
	// write files. A phone on a keypad is the worst possible place to grant that
	// by accident, so the read-only "plan" agent is the default and the operator
	// has to opt in explicitly (see toolPolicy in chat.go).
	AllowBuild bool
	AllowBash  bool
}

func loadConfig() (config, error) {
	c := config{
		Listen:       env("OCS40_LISTEN", ":443"),
		Admin:        env("OCS40_ADMIN", "127.0.0.1:8781"),
		Cert:         env("OCS40_CERT", "certs/server-chain.pem"),
		Key:          env("OCS40_KEY", "certs/server.key"),
		DBPath:       env("OCS40_DB", "data/ocs40.db"),
		OpenURL:      env("OPENCODE_URL", "http://127.0.0.1:4096"),
		Mock:         envBool("OCS40_MOCK", false),
		Project:      env("OPENCODE_PROJECT", ""),
		Model:        env("OCS40_MODEL", ""),
		Agent:        env("OCS40_AGENT", "plan"),
		System:       env("OCS40_SYSTEM", ""),
		RetentionDays: envInt("OCS40_RETENTION_DAYS", 30),
		DailyLimit:   envInt("OCS40_REQ_LIMIT", 100),
		Timeout:      time.Duration(envInt("OCS40_TIMEOUT_SEC", 120)) * time.Second,
		OpenTimeout:  time.Duration(envInt("OPENCODE_TIMEOUT_SEC", 15)) * time.Second,
		AllowBuild:   envBool("OCS40_ALLOW_BUILD", false),
		AllowBash:    envBool("OCS40_ALLOW_BASH", false),
	}
	if c.Agent == "" {
		c.Agent = "plan"
	}
	if err := os.MkdirAll(filepath.Dir(c.DBPath), 0o700); err != nil {
		return c, fmt.Errorf("data dir: %w", err)
	}
	return c, nil
}

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func envBool(k string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(k)))
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

func envInt(k string, def int) int {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
