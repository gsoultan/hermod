// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

// Package endpoint is the lineage API's endpoints: the request and response
// of each route, validated, and bound to the service. The HTTP transport
// decodes into these and encodes out of them; it holds no lineage logic.
package endpoint

import (
	"context"
	"errors"

	"github.com/gsoultan/hermod/internal/lineage"
	"github.com/gsoultan/hermod/internal/lineage/service"
)

// ErrInvalid is returned for a request that cannot be answered as sent.
var ErrInvalid = errors.New("lineage: invalid request")

// Summary counts a graph.
type Summary struct {
	Workflows int `json:"workflows"`
	Assets    int `json:"assets"`
	// Unresolved counts the assets known only by their record.
	Unresolved int `json:"unresolved"`
	Edges      int `json:"edges"`
}

// GraphRequest asks for the lineage graph of a scope.
type GraphRequest struct {
	Scope service.Scope
}

// GraphResponse is GET /api/vhosts/{vhost}/lineage.
type GraphResponse struct {
	VHost   string         `json:"vhost"`
	Nodes   []lineage.Node `json:"nodes"`
	Edges   []lineage.Edge `json:"edges"`
	Summary Summary        `json:"summary"`
}

// ImpactRequest asks for everything downstream of one node.
type ImpactRequest struct {
	Scope service.Scope
	Node  string
}

// ImpactResponse is GET /api/vhosts/{vhost}/lineage/impact.
type ImpactResponse struct {
	VHost string `json:"vhost"`
	lineage.Impact
}

// Endpoints are the lineage API's endpoints.
type Endpoints struct {
	Graph  func(context.Context, GraphRequest) (GraphResponse, error)
	Impact func(context.Context, ImpactRequest) (ImpactResponse, error)
}

// maxNodeID bounds ?node=. Asset ids are a kind, a system, a locator and an
// object name; none comes near this.
const maxNodeID = 2048

// New binds the endpoints to svc.
func New(svc *service.Service) Endpoints {
	return Endpoints{
		Graph: func(ctx context.Context, req GraphRequest) (GraphResponse, error) {
			g, err := svc.Graph(ctx, req.Scope)
			if err != nil {
				return GraphResponse{}, err
			}
			return GraphResponse{VHost: req.Scope.VHost, Nodes: g.Nodes, Edges: g.Edges, Summary: summarize(g)}, nil
		},
		Impact: func(ctx context.Context, req ImpactRequest) (ImpactResponse, error) {
			if req.Node == "" || len(req.Node) > maxNodeID {
				return ImpactResponse{}, ErrInvalid
			}
			imp, err := svc.Impact(ctx, req.Scope, req.Node)
			if err != nil {
				return ImpactResponse{}, err
			}
			return ImpactResponse{VHost: req.Scope.VHost, Impact: imp}, nil
		},
	}
}

func summarize(g lineage.Graph) Summary {
	s := Summary{Edges: len(g.Edges)}
	for _, n := range g.Nodes {
		if n.Kind == lineage.KindWorkflow {
			s.Workflows++
			continue
		}
		s.Assets++
		if !n.Resolved {
			s.Unresolved++
		}
	}
	return s
}
