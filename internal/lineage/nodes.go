// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package lineage

import (
	"encoding/json"
	"strings"

	"github.com/gsoultan/hermod/internal/storage"
)

// nodeIO is one asset a workflow node reads or writes.
type nodeIO struct {
	asset    asset
	relation string
	// out is true when the workflow writes the asset.
	out bool
}

// transType is the transformer a node runs: its transType, or, for a node
// whose type is the transformer itself, its type (the rule
// nodeTransformationType applies in workflow validation).
func transType(n storage.WorkflowNode) string {
	if tt, _ := n.Config["transType"].(string); tt != "" {
		return tt
	}
	return n.Type
}

func str(cfg map[string]any, key string) string {
	s, _ := cfg[key].(string)
	return strings.TrimSpace(s)
}

// sourceRef is the source a node names by id (storage.NodeConfigSourceKeys).
func sourceRef(cfg map[string]any) string {
	for _, k := range storage.NodeConfigSourceKeys {
		if id := str(cfg, k); id != "" {
			return id
		}
	}
	return ""
}

// objects decodes a list that may be stored as JSON text (as the editor saves
// pipeline steps and agent tools) or as a list.
func objects(raw any) []map[string]any {
	if s, ok := raw.(string); ok {
		if strings.TrimSpace(s) == "" {
			return nil
		}
		var out []map[string]any
		if json.Unmarshal([]byte(s), &out) != nil {
			return nil
		}
		return out
	}
	items, _ := raw.([]any)
	var out []map[string]any
	for _, it := range items {
		if m, ok := it.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// nodeAssets reads one workflow node.
type nodeReader struct {
	vhost   string
	sources map[string]storage.Source
}

func (r nodeReader) node(n storage.WorkflowNode) []nodeIO {
	if n.Type == "ai_agent" {
		var out []nodeIO
		// Only read tools carry an asset of their own; a sink tool writes to a
		// sink node of the same workflow, which is read as that node.
		for _, tool := range objects(n.Config["tools"]) {
			kind := str(tool, "kind")
			cfg, _ := tool["config"].(map[string]any)
			if kind != "sink" && kind != "mcp" && cfg != nil {
				out = append(out, r.transformer(kind, cfg)...)
			}
		}
		return out
	}
	return r.transformer(transType(n), n.Config)
}

// transformer reads the config of one transformer, or of each step of a
// pipeline.
func (r nodeReader) transformer(kind string, cfg map[string]any) []nodeIO {
	if kind == "pipeline" || kind == "parallel_pipeline" {
		var out []nodeIO
		for _, step := range objects(cfg["steps"]) {
			if st := str(step, "transType"); st != "pipeline" && st != "parallel_pipeline" {
				out = append(out, r.transformer(st, step)...)
			}
		}
		return out
	}
	if read, ok := transformerReaders[kind]; ok {
		return read(r, cfg)
	}
	return nil
}

// transformerReaders are the transformers that read or write an asset, by
// transType.
var transformerReaders = map[string]func(r nodeReader, cfg map[string]any) []nodeIO{
	"db_lookup": func(r nodeReader, cfg map[string]any) []nodeIO {
		if db, ok := r.sourceDB(sourceRef(cfg)); ok {
			return []nodeIO{{asset: tables(db, str(cfg, "table"))[0], relation: RelLooksUp}}
		}
		return nil
	},
	"execute_sql": func(r nodeReader, cfg map[string]any) []nodeIO {
		// The statement is free SQL; the database it runs in is what is known.
		if db, ok := r.sourceDB(sourceRef(cfg)); ok {
			return []nodeIO{{asset: db, relation: RelWrites, out: true}}
		}
		return nil
	},
	"scd": nodeReader.scd,
	"api_lookup": func(_ nodeReader, cfg map[string]any) []nodeIO {
		if loc, ok := urlLocator(str(cfg, "url")); ok {
			return []nodeIO{{asset: one(KindEndpoint, "http", loc, "")[0], relation: RelLooksUp}}
		}
		return nil
	},
	"reference_lookup": func(_ nodeReader, cfg map[string]any) []nodeIO {
		if p := str(cfg, "filePath"); p != "" {
			return []nodeIO{{asset: one(KindFile, "file", "", p)[0], relation: RelLooksUp}}
		}
		return nil
	},
	"ml_predict": func(r nodeReader, cfg map[string]any) []nodeIO {
		if m := str(cfg, "model"); m != "" {
			return []nodeIO{{asset: modelAsset(r.vhost, m), relation: RelPredicts}}
		}
		return nil
	},
	"ml_train":    nodeReader.train,
	"ai_retrieve": nodeReader.retrieve,
}

// scd writes its target and history tables in targetSourceId's database.
func (r nodeReader) scd(cfg map[string]any) []nodeIO {
	db, ok := r.sourceDB(str(cfg, "targetSourceId"))
	if !ok {
		return nil
	}
	var out []nodeIO
	for _, t := range []string{str(cfg, "targetTable"), str(cfg, "historyTable")} {
		if t != "" {
			out = append(out, nodeIO{asset: tables(db, t)[0], relation: RelWrites, out: true})
		}
	}
	return out
}

// train is ml_train: it trains model on dataset, refilling the dataset from
// sourceId's database first when one is set.
func (r nodeReader) train(cfg map[string]any) []nodeIO {
	var out []nodeIO
	dataset := str(cfg, "dataset")
	if db, ok := r.sourceDB(sourceRef(cfg)); ok {
		out = append(out, nodeIO{asset: db, relation: RelReads})
		if dataset != "" {
			out = append(out, nodeIO{asset: datasetAsset(r.vhost, dataset), relation: RelWrites, out: true})
		}
	}
	if dataset != "" {
		out = append(out, nodeIO{asset: datasetAsset(r.vhost, dataset), relation: RelReads})
	}
	if m := str(cfg, "model"); m != "" {
		out = append(out, nodeIO{asset: modelAsset(r.vhost, m), relation: RelTrains, out: true})
	}
	return out
}

// retrieve is ai_retrieve, on pgvector (connectionString, table) or pinecone
// (indexHost, namespace).
func (r nodeReader) retrieve(cfg map[string]any) []nodeIO {
	switch strings.ToLower(str(cfg, "store")) {
	case "pgvector":
		loc, ok := connLocator(cfgMap{"connection_string": str(cfg, "connectionString")}, false)
		if ok && str(cfg, "table") != "" {
			return []nodeIO{{asset: one(KindTable, "postgres", loc, str(cfg, "table"))[0], relation: RelRetrieves}}
		}
	case "pinecone":
		if h := hostOf(str(cfg, "indexHost")); h != "" {
			return []nodeIO{{asset: one(KindIndex, "pinecone", h, str(cfg, "namespace"))[0], relation: RelRetrieves}}
		}
	}
	return nil
}

// sourceDB is the database of a source a node queries, when the source is in
// the inputs.
func (r nodeReader) sourceDB(id string) (asset, bool) {
	src, ok := r.sources[id]
	if !ok {
		return asset{}, false
	}
	return sqlDatabase(src.Type, cfgMap(src.Config))
}
