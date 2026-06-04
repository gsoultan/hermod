package advanced

import (
	"context"
	"fmt"
	"github.com/user/hermod/pkg/comm/transformer"
	"strings"
	"sync"

	"github.com/user/hermod"
	lua "github.com/yuin/gopher-lua"
	"github.com/yuin/gopher-lua/parse"
)

func init() {
	transformer.Register("lua", &LuaTransformer{
		pool: &sync.Pool{
			New: func() any {
				L := lua.NewState()
				return L
			},
		},
		cache: make(map[string]*lua.FunctionProto),
	})
}

type LuaTransformer struct {
	pool  *sync.Pool
	mu    sync.RWMutex
	cache map[string]*lua.FunctionProto
}

func (t *LuaTransformer) getProto(script string) (*lua.FunctionProto, error) {
	t.mu.RLock()
	proto, ok := t.cache[script]
	t.mu.RUnlock()
	if ok {
		return proto, nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if proto, ok = t.cache[script]; ok {
		return proto, nil
	}

	reader := strings.NewReader(script)
	chunk, err := parse.Parse(reader, "<string>")
	if err != nil {
		return nil, err
	}
	proto, err = lua.Compile(chunk, "<string>")
	if err != nil {
		return nil, err
	}
	t.cache[script] = proto
	return proto, nil
}

func (t *LuaTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	script, _ := config["script"].(string)
	if script == "" {
		return msg, nil
	}

	L := t.pool.Get().(*lua.LState)
	defer t.pool.Put(L)

	// Set message data as a global table
	msgTable := L.NewTable()
	for k, v := range msg.Data() {
		msgTable.RawSetString(k, t.toLValue(L, v))
	}
	L.SetGlobal("msg", msgTable)

	// Set metadata as a global table
	metaTable := L.NewTable()
	for k, v := range msg.Metadata() {
		metaTable.RawSetString(k, lua.LString(v))
	}
	L.SetGlobal("metadata", metaTable)

	// Execute script (using cached proto for performance)
	proto, err := t.getProto(script)
	if err != nil {
		return nil, fmt.Errorf("lua compilation error: %w", err)
	}
	lfunc := L.NewFunctionFromProto(proto)
	L.Push(lfunc)
	if err := L.PCall(0, lua.MultRet, nil); err != nil {
		return nil, fmt.Errorf("lua script error: %w", err)
	}

	// Read modified data back from msg table
	newMsgTable := L.GetGlobal("msg")
	if tbl, ok := newMsgTable.(*lua.LTable); ok {
		msg.ClearPayloads()
		tbl.ForEach(func(k, v lua.LValue) {
			msg.SetData(k.String(), t.fromLValue(v))
		})
	}

	// Read modified metadata back
	newMetaTable := L.GetGlobal("metadata")
	if tbl, ok := newMetaTable.(*lua.LTable); ok {
		tbl.ForEach(func(k, v lua.LValue) {
			msg.SetMetadata(k.String(), v.String())
		})
	}

	return msg, nil
}

func (t *LuaTransformer) toLValue(L *lua.LState, v any) lua.LValue {
	switch val := v.(type) {
	case string:
		return lua.LString(val)
	case float64:
		return lua.LNumber(val)
	case int:
		return lua.LNumber(val)
	case bool:
		return lua.LBool(val)
	case map[string]any:
		tbl := L.NewTable()
		for k, v2 := range val {
			tbl.RawSetString(k, t.toLValue(L, v2))
		}
		return tbl
	case []any:
		tbl := L.NewTable()
		for i, v2 := range val {
			tbl.RawSetInt(i+1, t.toLValue(L, v2))
		}
		return tbl
	default:
		return lua.LNil
	}
}

func (t *LuaTransformer) fromLValue(v lua.LValue) any {
	switch val := v.(type) {
	case lua.LString:
		return string(val)
	case lua.LNumber:
		return float64(val)
	case lua.LBool:
		return bool(val)
	case *lua.LTable:
		// Check if it's an array or map
		isArr := true
		maxIdx := 0
		val.ForEach(func(k, v lua.LValue) {
			if idx, ok := k.(lua.LNumber); ok && float64(idx) == float64(int(idx)) && idx > 0 {
				if int(idx) > maxIdx {
					maxIdx = int(idx)
				}
			} else {
				isArr = false
			}
		})

		if isArr && maxIdx > 0 {
			arr := make([]any, maxIdx)
			val.ForEach(func(k, v lua.LValue) {
				arr[int(k.(lua.LNumber))-1] = t.fromLValue(v)
			})
			return arr
		}

		m := make(map[string]any)
		val.ForEach(func(k, v lua.LValue) {
			m[k.String()] = t.fromLValue(v)
		})
		return m
	default:
		return nil
	}
}
