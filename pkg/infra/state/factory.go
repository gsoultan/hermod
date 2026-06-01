package state

import (
	"fmt"
	"time"

	"github.com/user/hermod"
)

type Config struct {
	Type     string
	Path     string
	Address  string
	Password string
	DB       int
	Prefix   string
}

func NewStateStore(cfg Config) (hermod.StateStore, error) {
	switch cfg.Type {
	case "sqlite":
		if cfg.Path == "" {
			cfg.Path = "hermod_state.db"
		}
		return NewSQLiteStateStore(cfg.Path)
	case "redis":
		return NewRedisStateStore(cfg.Address, cfg.Password, cfg.DB, cfg.Prefix, 0), nil
	case "etcd":
		return NewEtcdStateStore([]string{cfg.Address}, cfg.Prefix, 5*time.Second)
	case "":
		return NewSQLiteStateStore("hermod_state.db")
	default:
		return nil, fmt.Errorf("unsupported state store type: %s", cfg.Type)
	}
}
