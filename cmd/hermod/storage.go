package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/storage"
	storagemongo "github.com/user/hermod/internal/storage/mongodb"
	storagepebble "github.com/user/hermod/internal/storage/pebble"
	storagesql "github.com/user/hermod/internal/storage/sql"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type userLister interface {
	ListUsers(ctx context.Context, filter storage.CommonFilter) ([]storage.User, int, error)
}

func computeSetupStatus(ctx context.Context, store userLister, configuredFlag bool) (bool, bool) {
	if !configuredFlag {
		return false, false
	}
	if store == nil {
		return true, true
	}
	users, _, err := store.ListUsers(ctx, storage.CommonFilter{Limit: 1})
	if err != nil {
		return true, true
	}
	return true, len(users) > 0
}

func initStorage(dbType, dbConn string) (storage.Storage, error) {
	var store storage.Storage
	var err error

	switch dbType {
	case "mongodb", "pebble":
		store, err = initNoSQLStorage(dbType, dbConn)
	default:
		store, err = initSQLStorage(dbType, dbConn)
	}

	if err != nil {
		return store, err
	}
	return postInitStorage(store)
}

func initNoSQLStorage(dbType, dbConn string) (storage.Storage, error) {
	if dbType == "mongodb" {
		client, err := mongo.Connect(options.Client().ApplyURI(dbConn))
		if err != nil {
			return nil, fmt.Errorf("failed to connect to MongoDB: %w", err)
		}
		dbName := "hermod"
		if parts := strings.Split(dbConn, "/"); len(parts) > 3 {
			dbName = strings.Split(parts[3], "?")[0]
		}
		return storagemongo.NewMongoStorage(client, dbName), nil
	}
	store, err := storagepebble.NewPebbleStorage(dbConn)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Pebble storage: %w", err)
	}
	return store, nil
}

func initSQLStorage(dbType, dbConn string) (storage.Storage, error) {
	driver, conn := getSQLDriverAndConn(dbType, dbConn)
	db, err := sql.Open(driver, conn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	configureSQLDB(db, dbType)
	return storagesql.NewSQLStorage(db, driver), nil
}

func getSQLDriverAndConn(dbType, dbConn string) (string, string) {
	switch dbType {
	case "sqlite":
		if !strings.Contains(dbConn, "?") {
			busy := os.Getenv("HERMOD_SQLITE_BUSY_TIMEOUT_MS")
			if busy == "" {
				busy = "2000"
			}
			dbConn += fmt.Sprintf("?_pragma=journal_mode(WAL)&_pragma=busy_timeout(%s)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)", busy)
		}
		return "sqlite", dbConn
	case "postgres":
		return "pgx", dbConn
	case "mysql", "mariadb":
		return "mysql", dbConn
	}
	return "", dbConn
}

func configureSQLDB(db *sql.DB, dbType string) {
	if dbType == "sqlite" {
		db.SetMaxOpenConns(4)
		db.SetMaxIdleConns(1)
	} else {
		db.SetMaxOpenConns(20)
		db.SetMaxIdleConns(10)
		db.SetConnMaxIdleTime(60 * time.Second)
	}
}

func postInitStorage(store storage.Storage) (storage.Storage, error) {
	s, ok := store.(interface{ Init(context.Context) error })
	if !ok {
		return store, nil
	}
	initTimeout := 5000
	if v := os.Getenv("HERMOD_STORAGE_INIT_TIMEOUT_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			initTimeout = n
		}
	}
	ctx := context.Background()
	if initTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(initTimeout)*time.Millisecond)
		defer cancel()
	}
	if err := s.Init(ctx); err != nil {
		return store, fmt.Errorf("failed to initialize storage: %w", err)
	}
	return store, nil
}

func retryInit(ctx context.Context, store storage.Storage, name string, logger hermod.Logger) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := store.Init(ctx); err == nil {
				logger.Info("Successfully initialized storage after retry", "type", name)
				return
			}
		}
	}
}
