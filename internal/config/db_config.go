package config

import (
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gopkg.in/yaml.v3"
)

type DBConfig struct {
	Type            string `yaml:"type" json:"type"`
	Conn            string `yaml:"conn" json:"conn"`
	LogType         string `yaml:"log_type" json:"log_type"`
	LogConn         string `yaml:"log_conn" json:"log_conn"`
	JWTSecret       string `yaml:"jwt_secret" json:"jwt_secret"`
	CryptoMasterKey string `yaml:"crypto_master_key" json:"crypto_master_key"`
}

func getDBConfigPath() string {
	return GetConfigPath("db_config.yaml")
}

// dbConfigEntry memoises the file-derived half of LoadDBConfig.
//
// The auth middleware calls LoadDBConfig once per authenticated request to get
// the JWT signing secret, so reading and parsing this YAML sat on the hot path
// of every API call — about 14µs, two thirds of the middleware's total work.
//
// Only what the *file* determines is cached. Environment overrides are applied
// fresh on every call, so nothing that can change at runtime is frozen, and the
// entry is keyed on the file's identity, modification time and size together
// with the values of any ${VAR} the file interpolates. An edit or an environment
// change is therefore picked up on the next call exactly as it was before.
//
// mtime and size are how the file is identified, which assumes a filesystem
// with sub-second timestamps — true of APFS, ext4 and NTFS. On one with
// whole-second granularity, two edits of identical length inside the same second
// could be missed.
type dbConfigEntry struct {
	path    string
	modTime time.Time
	size    int64
	envKeys []string
	envVals string
	cfg     DBConfig
}

var (
	dbConfigMu    sync.Mutex
	dbConfigCache *dbConfigEntry

	// dbConfigParses counts actual reads, so a test can prove the cache works
	// rather than infer it from a timing measurement.
	dbConfigParses atomic.Int64
)

// referencedEnvKeys returns the distinct ${VAR} names the raw content refers to.
func referencedEnvKeys(raw string) []string {
	matches := envRegex.FindAllStringSubmatch(raw, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(matches))
	keys := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) > 1 && !seen[m[1]] {
			seen[m[1]] = true
			keys = append(keys, m[1])
		}
	}
	return keys
}

// envSnapshot renders the current values of keys, so a change to any of them
// invalidates the entry. Unset is distinguished from empty because the two
// substitute differently.
func envSnapshot(keys []string) string {
	if len(keys) == 0 {
		return ""
	}
	var b strings.Builder
	for _, k := range keys {
		v, ok := os.LookupEnv(k)
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(v)
		if ok {
			b.WriteString("\x01")
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// dbConfigFromFile returns the file-derived config, the error from reading the
// file (which the caller interprets), and any parse error.
func dbConfigFromFile(path string) (cfg DBConfig, readErr, parseErr error) {
	info, statErr := os.Stat(path)

	dbConfigMu.Lock()
	defer dbConfigMu.Unlock()

	if c := dbConfigCache; c != nil && statErr == nil &&
		c.path == path && c.size == info.Size() && c.modTime.Equal(info.ModTime()) &&
		c.envVals == envSnapshot(c.envKeys) {
		// A copy: callers mutate what LoadDBConfig hands back — rotating the
		// master key loads, edits and saves — and an edit must not leak into
		// every later read.
		return c.cfg, nil, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		dbConfigCache = nil
		return DBConfig{}, err, nil
	}

	raw := string(data)
	if err := yaml.Unmarshal([]byte(SubstituteEnvVars(raw)), &cfg); err != nil {
		dbConfigCache = nil
		return DBConfig{}, nil, err
	}
	dbConfigParses.Add(1)

	if statErr == nil {
		keys := referencedEnvKeys(raw)
		dbConfigCache = &dbConfigEntry{
			path:    path,
			modTime: info.ModTime(),
			size:    info.Size(),
			envKeys: keys,
			envVals: envSnapshot(keys),
			cfg:     cfg,
		}
	} else {
		dbConfigCache = nil
	}
	return cfg, nil, nil
}

func LoadDBConfig() (*DBConfig, error) {
	path := getDBConfigPath()

	cfg, err, parseErr := dbConfigFromFile(path)
	if parseErr != nil {
		return nil, parseErr
	}
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	// Environment variable overrides (highest precedence)
	if v := os.Getenv("HERMOD_DB_TYPE"); v != "" {
		cfg.Type = v
	}
	if v := os.Getenv("HERMOD_DB_CONN"); v != "" {
		cfg.Conn = v
	}
	if v := os.Getenv("HERMOD_LOG_DB_TYPE"); v != "" {
		cfg.LogType = v
	}
	if v := os.Getenv("HERMOD_LOG_DB_CONN"); v != "" {
		cfg.LogConn = v
	}
	if v := os.Getenv("HERMOD_JWT_SECRET"); v != "" {
		cfg.JWTSecret = v
	}
	if v := os.Getenv("HERMOD_MASTER_KEY"); v != "" {
		cfg.CryptoMasterKey = v
	}

	// If everything is empty and file was missing, return original error
	if cfg.Type == "" && cfg.Conn == "" && err != nil {
		return nil, err
	}

	return &cfg, nil
}

func SaveDBConfig(cfg *DBConfig) error {
	if err := EnsureConfigDir(); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	// db_config.yaml holds sensitive secrets (JWTSecret, CryptoMasterKey),
	// so it must not be world/group readable.
	return os.WriteFile(getDBConfigPath(), data, 0600)
}

func IsDBConfigured() bool {
	if _, err := os.Stat(getDBConfigPath()); err == nil {
		return true
	}
	// Also considered configured if minimal environment variables are set
	return os.Getenv("HERMOD_DB_TYPE") != "" && os.Getenv("HERMOD_DB_CONN") != ""
}
