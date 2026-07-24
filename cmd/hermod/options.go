package main

import (
	"flag"
	"os"
	"strconv"

	"github.com/user/hermod/internal/config"
)

type Options struct {
	mode               string
	workerID           int
	totalWorkers       int
	workerGUID         string
	workerToken        string
	platformURL        string
	workerHost         string
	workerPort         int
	workerDescription  string
	workerIdentityPath string
	initWorker         bool
	buildUI            bool
	port               int
	grpcPort           int
	dbType             string
	dbConn             string
	logDBType          string
	logDBConn          string
	masterKey          string
	configPath         string
	versionFlag        bool
	serviceAction      string
	disableAutoscaler  bool
}

func parseOptions() *Options {
	o := &Options{}
	o.defineWorkerFlags()
	o.defineDBFlags()
	o.defineOtherFlags()
	flag.Parse()
	o.applyEnvOverrides()
	return o
}

func (o *Options) defineWorkerFlags() {
	flag.StringVar(&o.mode, "mode", "standalone", "running mode: standalone, api, worker")
	flag.IntVar(&o.workerID, "worker-id", 0, "ID of the worker (0 to total-workers-1)")
	flag.IntVar(&o.totalWorkers, "total-workers", 1, "total number of workers for sharding")
	flag.StringVar(&o.workerGUID, "worker-guid", "", "GUID of the worker for explicit assignment")
	flag.StringVar(&o.workerToken, "worker-token", "", "Security token for the worker")
	flag.StringVar(&o.platformURL, "platform-url", "", "URL of the Hermod platform API (e.g., http://localhost:8080)")
	flag.StringVar(&o.workerHost, "worker-host", "localhost", "host of the worker for self-registration")
	flag.IntVar(&o.workerPort, "worker-port", 3000, "port of the worker for self-registration")
	flag.StringVar(&o.workerDescription, "worker-description", "", "description of the worker for self-registration")
	flag.StringVar(&o.workerIdentityPath, "worker-identity", config.GetConfigPath("worker.yaml"), "path to persist worker identity (id/token)")
	flag.BoolVar(&o.initWorker, "init-worker", false, "initialize/register a worker locally (DB mode) and save identity, then exit")
}

func (o *Options) defineDBFlags() {
	flag.StringVar(&o.dbType, "db-type", "sqlite", "database type: sqlite, postgres, mysql, mariadb, mongodb, pebble")
	flag.StringVar(&o.dbConn, "db-conn", config.GetConfigPath("hermod.db"), "database connection string")
	flag.StringVar(&o.logDBType, "log-db-type", "", "database type for logging (defaults to main db type)")
	flag.StringVar(&o.logDBConn, "log-db-conn", "", "database connection string for logging (defaults to main db conn)")
}

func (o *Options) defineOtherFlags() {
	flag.BoolVar(&o.buildUI, "build-ui", false, "build UI before starting")
	flag.IntVar(&o.port, "port", 4000, "port for API server")
	flag.IntVar(&o.grpcPort, "grpc-port", 50051, "port for gRPC server")
	flag.StringVar(&o.masterKey, "master-key", "", "Master key for encryption (32 bytes)")
	flag.StringVar(&o.configPath, "config", config.GetConfigPath("config.yaml"), "path to engine configuration file")
	flag.BoolVar(&o.versionFlag, "version", false, "Print the version and exit")
	flag.StringVar(&o.serviceAction, "service", "", "Service action: install, uninstall, start, stop, restart, status")
	flag.BoolVar(&o.disableAutoscaler, "disable-autoscaler", false, "disable the autoscaler service")
}

func (o *Options) applyEnvOverrides() {
	o.applyWorkerEnvOverrides()
	o.applyDBEnvOverrides()
	o.applyOtherEnvOverrides()
}

func (o *Options) applyWorkerEnvOverrides() {
	if v := os.Getenv("HERMOD_MODE"); v != "" && o.mode == "standalone" {
		o.mode = v
	}
	if v := os.Getenv("HERMOD_WORKER_GUID"); v != "" && o.workerGUID == "" {
		o.workerGUID = v
	}
	if v := os.Getenv("HERMOD_WORKER_TOKEN"); v != "" && o.workerToken == "" {
		o.workerToken = v
	}
	if v := os.Getenv("HERMOD_PLATFORM_URL"); v != "" && o.platformURL == "" {
		o.platformURL = v
	}
	if v := os.Getenv("HERMOD_WORKER_HOST"); v != "" && o.workerHost == "localhost" {
		o.workerHost = v
	}
	if v := os.Getenv("HERMOD_WORKER_PORT"); v != "" && o.workerPort == 3000 {
		if p, err := strconv.Atoi(v); err == nil {
			o.workerPort = p
		}
	}
	o.applyWorkerIdentityEnvOverrides()
}

func (o *Options) applyWorkerIdentityEnvOverrides() {
	if v := os.Getenv("HERMOD_WORKER_DESCRIPTION"); v != "" && o.workerDescription == "" {
		o.workerDescription = v
	}
	if v := os.Getenv("HERMOD_WORKER_IDENTITY"); v != "" && o.workerIdentityPath == config.GetConfigPath("worker.yaml") {
		o.workerIdentityPath = v
	}
	if v := os.Getenv("HERMOD_WORKER_ID"); v != "" && o.workerID == 0 {
		if id, err := strconv.Atoi(v); err == nil {
			o.workerID = id
		}
	}
	if v := os.Getenv("HERMOD_TOTAL_WORKERS"); v != "" && o.totalWorkers == 1 {
		if tw, err := strconv.Atoi(v); err == nil {
			o.totalWorkers = tw
		}
	}
}

func (o *Options) applyDBEnvOverrides() {
	if v := os.Getenv("HERMOD_DB_TYPE"); v != "" && o.dbType == "sqlite" {
		o.dbType = v
	}
	if v := os.Getenv("HERMOD_DB_CONN"); v != "" && o.dbConn == config.GetConfigPath("hermod.db") {
		o.dbConn = v
	}
	if v := os.Getenv("HERMOD_LOG_DB_TYPE"); v != "" && o.logDBType == "" {
		o.logDBType = v
	}
	if v := os.Getenv("HERMOD_LOG_DB_CONN"); v != "" && o.logDBConn == "" {
		o.logDBConn = v
	}
}

func (o *Options) applyOtherEnvOverrides() {
	if v := os.Getenv("HERMOD_PORT"); v != "" && o.port == 4000 {
		if p, err := strconv.Atoi(v); err == nil {
			o.port = p
		}
	}
	if v := os.Getenv("HERMOD_GRPC_PORT"); v != "" && o.grpcPort == 50051 {
		if p, err := strconv.Atoi(v); err == nil {
			o.grpcPort = p
		}
	}
	if v := os.Getenv("HERMOD_CONFIG"); v != "" && o.configPath == config.GetConfigPath("config.yaml") {
		o.configPath = v
	}
	if v := os.Getenv("HERMOD_MASTER_KEY"); v != "" && o.masterKey == "" {
		o.masterKey = v
	}
	if v := os.Getenv("HERMOD_DISABLE_AUTOSCALER"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			o.disableAutoscaler = b
		}
	}
}
