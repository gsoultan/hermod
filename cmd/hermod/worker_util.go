package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"

	"github.com/google/uuid"
	"github.com/user/hermod/internal/engine/registry"
	"github.com/user/hermod/internal/engine/worker"
	"github.com/user/hermod/internal/storage"
	"gopkg.in/yaml.v3"
)

type workerIdentity struct {
	ID          string `yaml:"id"`
	Token       string `yaml:"token"`
	Name        string `yaml:"name"`
	Host        string `yaml:"host"`
	Port        int    `yaml:"port"`
	Description string `yaml:"description"`
}

func setupWorker(ctx context.Context, o *Options, reg *registry.Registry, store storage.Storage, configured, userSetup bool) *worker.Worker {
	if !shouldStartWorker(o, configured, userSetup) {
		return nil
	}

	workerStore := getWorkerStore(o, store)
	if workerStore == nil {
		return nil
	}

	handleWorkerIdentity(ctx, o, store)
	wrk := worker.NewWorker(workerStore, reg)
	wrk.SetWorkerConfig(o.workerID, o.totalWorkers, o.workerGUID, o.workerToken)
	wrk.SetRegistrationInfo(getWorkerName(o.workerGUID), o.workerHost, o.workerPort, o.workerDescription)

	startWorkerAsync(ctx, wrk)
	return wrk
}

func shouldStartWorker(o *Options, configured, userSetup bool) bool {
	return (o.mode == "worker" || o.mode == "standalone") && configured && userSetup
}

func getWorkerStore(o *Options, store storage.Storage) worker.WorkerStorage {
	if o.mode == "worker" && o.platformURL != "" {
		return worker.NewWorkerAPIClient(o.platformURL, o.workerToken)
	}
	return store
}

func getWorkerName(guid string) string {
	name, _ := os.Hostname()
	if name == "" {
		return guid
	}
	return name
}

func startWorkerAsync(ctx context.Context, wrk *worker.Worker) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("CRITICAL: Worker process panicked: %v\n%s", r, string(debug.Stack()))
			}
		}()
		if err := wrk.Start(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("Worker failed: %v", err)
		}
	}()
}

func handleWorkerIdentity(ctx context.Context, o *Options, store storage.Storage) {
	if (o.workerGUID == "" || o.workerToken == "") && o.platformURL == "" {
		if !loadWorkerIdentity(o) {
			createWorkerIdentity(ctx, o, store)
		}
	}

	if o.initWorker {
		if o.platformURL != "" {
			log.Fatal("-init-worker is only supported in local DB mode (no platform-url).")
		}
		if o.workerGUID == "" || o.workerToken == "" {
			log.Fatal("Failed to initialize worker: missing GUID or token.")
		}
		fmt.Printf("Worker initialized.\nID: %s\nToken: %s\n", o.workerGUID, o.workerToken)
		os.Exit(0)
	}
}

func loadWorkerIdentity(o *Options) bool {
	b, err := os.ReadFile(o.workerIdentityPath)
	if err != nil {
		return false
	}
	var wi workerIdentity
	if err := yaml.Unmarshal(b, &wi); err != nil || wi.ID == "" || wi.Token == "" {
		return false
	}
	o.workerGUID = wi.ID
	o.workerToken = wi.Token
	if wi.Name != "" {
		o.workerDescription = wi.Description
		o.workerHost = wi.Host
		if wi.Port != 0 {
			o.workerPort = wi.Port
		}
	}
	fmt.Printf("Loaded worker identity from %s (id=%s)\n", o.workerIdentityPath, wi.ID)
	return true
}

func createWorkerIdentity(ctx context.Context, o *Options, store storage.Storage) {
	if store == nil {
		return
	}
	id, token := uuid.New().String(), uuid.New().String()
	name, _ := os.Hostname()
	if name == "" {
		name = id
	}
	err := store.CreateWorker(ctx, storage.Worker{
		ID: id, Name: name, Host: o.workerHost, Port: o.workerPort, Description: o.workerDescription, Token: token,
	})
	if err != nil {
		log.Printf("Warning: failed to auto-create worker identity: %v", err)
		return
	}
	o.workerGUID, o.workerToken = id, token
	saveWorkerIdentity(o, id, token, name)
}

func saveWorkerIdentity(o *Options, id, token, name string) {
	wi := workerIdentity{
		ID: id, Token: token, Name: name, Host: o.workerHost, Port: o.workerPort, Description: o.workerDescription,
	}
	if data, err := yaml.Marshal(&wi); err == nil {
		_ = os.MkdirAll(filepath.Dir(o.workerIdentityPath), 0o755)
		_ = os.WriteFile(o.workerIdentityPath, data, 0o600)
		fmt.Printf("Saved worker identity to %s\n", o.workerIdentityPath)
	}
}
