package transformer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/user/hermod"
)

func init() {
	Register("wasm", &WasmTransformer{
		cache: make(map[string]wazero.CompiledModule),
	})
}

type WasmTransformer struct {
	mu      sync.RWMutex
	runtime wazero.Runtime
	cache   map[string]wazero.CompiledModule
}

func (t *WasmTransformer) getRuntime(ctx context.Context) wazero.Runtime {
	t.mu.RLock()
	if t.runtime != nil {
		defer t.mu.RUnlock()
		return t.runtime
	}
	t.mu.RUnlock()

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.runtime == nil {
		t.runtime = wazero.NewRuntime(ctx)
		wasi_snapshot_preview1.MustInstantiate(ctx, t.runtime)
	}
	return t.runtime
}

func (t *WasmTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	functionName, _ := config["function"].(string)
	if functionName == "" {
		functionName = "transform"
	}

	var bin []byte
	pluginID, _ := config["pluginID"].(string)
	if pluginID != "" {
		// Load from local cache
		cachePath := filepath.Join("data", "plugins", pluginID+".wasm")
		var err error
		bin, err = os.ReadFile(cachePath)
		if err != nil {
			return msg, fmt.Errorf("failed to read installed plugin %s: %w", pluginID, err)
		}
	} else {
		wasmURL, _ := config["wasmURL"].(string)
		if wasmURL != "" {
			// Fetch from URL
			// For enterprise, we should cache the fetched binary or use a proper downloader
			resp, err := http.Get(wasmURL)
			if err != nil {
				return msg, fmt.Errorf("failed to fetch wasm from url: %w", err)
			}
			defer resp.Body.Close()
			bin, err = io.ReadAll(resp.Body)
			if err != nil {
				return msg, fmt.Errorf("failed to read wasm body: %w", err)
			}
		} else if b, ok := config["wasmBytes"].([]byte); ok {
			bin = b
		} else if s, ok := config["wasmBytes"].(string); ok {
			// If it's a string, it might be base64
			bin = []byte(s)
		}
	}

	if len(bin) == 0 {
		return msg, fmt.Errorf("wasm binary not provided")
	}

	cacheKey := fmt.Sprintf("%x", bin)

	r := t.getRuntime(ctx)

	t.mu.RLock()
	compiled, ok := t.cache[cacheKey]
	t.mu.RUnlock()

	if !ok {
		t.mu.Lock()
		if compiled, ok = t.cache[cacheKey]; !ok {
			var err error
			compiled, err = r.CompileModule(ctx, bin)
			if err != nil {
				t.mu.Unlock()
				return msg, fmt.Errorf("failed to compile wasm module: %w", err)
			}
			t.cache[cacheKey] = compiled
		}
		t.mu.Unlock()
	}

	// Prepare data for WASM
	inputData, err := json.Marshal(msg.Data())
	if err != nil {
		return msg, err
	}

	stdout := &wasmBuffer{}
	modCfg := wazero.NewModuleConfig().
		WithStdin(io.NopCloser(wasmReader(inputData))).
		WithStdout(stdout).
		WithStderr(io.Discard)

	mod, err := r.InstantiateModule(ctx, compiled, modCfg)
	if err != nil {
		return msg, fmt.Errorf("failed to instantiate wasm module: %w", err)
	}
	defer mod.Close(ctx)

	f := mod.ExportedFunction(functionName)
	if f == nil {
		return msg, fmt.Errorf("wasm function %s not found", functionName)
	}

	_, err = f.Call(ctx)
	if err != nil {
		return msg, fmt.Errorf("wasm execution failed: %w", err)
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return msg, fmt.Errorf("failed to parse wasm output: %w", err)
	}

	for k, v := range result {
		msg.SetData(k, v)
	}

	return msg, nil
}

type wasmBuffer struct {
	buf []byte
	mu  sync.Mutex
}

func (b *wasmBuffer) Write(p []byte) (n int, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *wasmBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf
}

func wasmReader(data []byte) io.Reader {
	return &readerWrapper{data: data}
}

type readerWrapper struct {
	data []byte
	off  int
}

func (r *readerWrapper) Read(p []byte) (n int, err error) {
	if r.off >= len(r.data) {
		return 0, io.EOF
	}
	n = copy(p, r.data[r.off:])
	r.off += n
	return n, nil
}
