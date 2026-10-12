package grpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/gsoultan/hermod/internal/ml"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/proto"
	"github.com/gsoultan/hermod/pkg/ml/recommend"
)

// RecommendScenario authenticates the caller as Predict does, then asks a
// scenario model for its answer, as POST /api/ml/serve/{vhost}/{name}/recommend
// does.
func (s *Server) RecommendScenario(ctx context.Context, req *proto.RecommendScenarioRequest) (*proto.RecommendScenarioResponse, error) {
	svc := s.ServiceFunc()
	if svc == nil {
		return nil, status.Error(codes.Unavailable, "Hermod is not set up yet")
	}
	if err := svc.AuthorizeServing(ctx, req.GetVhost(), req.GetModel(), apiKey(ctx)); err != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid serving key")
	}

	call, err := scenarioRequest(req)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	out, err := svc.RecommendScenario(ml.WithCaller(ctx, storage.MLCallerGRPC, ""), req.GetVhost(), req.GetModel(), call)
	if err != nil {
		return nil, recommendStatus(err)
	}
	resp := &proto.RecommendScenarioResponse{Model: req.GetModel(), RecommendationId: out.RecommendationID,
		Results: make([]*proto.ScenarioResult, len(out.Results))}
	for i, r := range out.Results {
		res := &proto.ScenarioResult{Subject: r.For}
		if res.Items, err = picks(r.Items); err != nil {
			return nil, err
		}
		for _, d := range r.Plan {
			items, err := picks(d.Items)
			if err != nil {
				return nil, err
			}
			res.Plan = append(res.Plan, &proto.PlanDay{Date: d.Date, Items: items})
		}
		resp.Results[i] = res
	}
	return resp, nil
}

// scenarioRequest is the JSON call's request for req. Empty strings are
// subjects not given.
func scenarioRequest(req *proto.RecommendScenarioRequest) (ml.ScenarioRequest, error) {
	var call ml.ScenarioRequest
	sub := req.GetSubject()
	set := func(v string) any {
		if v == "" {
			return nil
		}
		return v
	}
	call.For = ml.ScenarioFor{Customer: set(sub.GetCustomer()), Product: set(sub.GetProduct()),
		Salesman: set(sub.GetSalesman()), Territory: set(sub.GetTerritory())}
	for _, c := range sub.GetCustomers() {
		call.For.Customers = append(call.For.Customers, c)
	}
	if req.GetPeriodFrom() != "" || req.GetPeriodTo() != "" {
		call.Period = &ml.ScenarioPeriod{From: req.GetPeriodFrom(), To: req.GetPeriodTo()}
	}
	call.K = int(req.GetK())
	call.ExcludeSeen = req.ExcludeSeen
	for i, r := range req.GetRules() {
		raw, err := json.Marshal(r.AsMap())
		if err != nil {
			return call, err
		}
		var rule recommend.Rule
		if err := json.Unmarshal(raw, &rule); err != nil {
			return call, fmt.Errorf("rule %d: %w", i+1, err)
		}
		call.Rules = append(call.Rules, rule)
	}
	return call, nil
}

func picks(in []recommend.Pick) ([]*proto.ScenarioPick, error) {
	out := make([]*proto.ScenarioPick, len(in))
	for i, p := range in {
		pick := &proto.ScenarioPick{Id: p.ID, Score: p.Score, Rank: int32(p.Rank), DueDate: p.DueDate}
		for _, r := range p.Reasons {
			pick.Reasons = append(pick.Reasons, &proto.Reason{Code: r.Code, Text: r.Text})
		}
		if len(p.Fields) > 0 {
			// Through JSON, so a field of any JSON type (a time, a number
			// type structpb does not know) arrives as the REST answer has it.
			raw, err := json.Marshal(p.Fields)
			if err != nil {
				return nil, status.Errorf(codes.Internal, "item %s cannot be sent: %v", p.ID, err)
			}
			fields := &structpb.Struct{}
			if err := fields.UnmarshalJSON(raw); err != nil {
				return nil, status.Errorf(codes.Internal, "item %s cannot be sent: %v", p.ID, err)
			}
			pick.Fields = fields
		}
		out[i] = pick
	}
	return out, nil
}

// recommendStatus maps a recommendation error onto a gRPC status.
func recommendStatus(err error) error {
	switch {
	case errors.Is(err, ml.ErrBadRecommendRequest), errors.Is(err, ml.ErrNotRecommender):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, ml.ErrQuotaExceeded):
		return status.Error(codes.ResourceExhausted, err.Error())
	}
	return status.Error(codes.Unavailable, err.Error())
}
