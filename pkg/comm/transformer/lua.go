package transformer

import (
	"context"
	"fmt"
	"sync"

	"github.com/user/hermod"
	lua "github.com/yuin/gopher-lua"
)

func init() {
	Register("lua", &LuaTransformer{
		pool: &sync.Pool{
			New: func() any {
				L := lua.NewState()
				return L
			},
		},
	})
}

type LuaTransformer struct {
	pool *sync.Pool
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

	// Execute script
	if err := L.DoString(script); err != nil {
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
