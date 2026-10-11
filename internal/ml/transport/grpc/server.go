// Package grpc serves hermod.ml.v1 InferenceService: the gRPC twin of
// POST /api/ml/serve/{vhost}/{name}.
package grpc

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/gsoultan/hermod/internal/ml"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/inference"
	"github.com/gsoultan/hermod/pkg/ml/proto"
)

// Server implements proto.InferenceServiceServer.
type Server struct {
	proto.UnimplementedInferenceServiceServer
	// ServiceFunc returns the ML service to use, looked up per call because
	// first-run setup and a database switch replace the store while the gRPC
	// server keeps running.
	ServiceFunc func() *ml.Service
}

// Predict authenticates the caller with the model's serving key, then runs the
// rows through the model.
func (s *Server) Predict(ctx context.Context, req *proto.PredictRequest) (*proto.PredictResponse, error) {
	svc := s.ServiceFunc()
	if svc == nil {
		return nil, status.Error(codes.Unavailable, "Hermod is not set up yet")
	}
	// One answer for every refusal, as the REST endpoint gives: the service
	// does not say which models exist or have serving turned on.
	if err := svc.AuthorizeServing(ctx, req.GetVhost(), req.GetModel(), apiKey(ctx)); err != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid serving key")
	}

	if len(req.GetInstances()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "instances must hold at least one row")
	}
	if len(req.GetInstances()) > ml.MaxRowsPerCall {
		return nil, status.Error(codes.InvalidArgument, ml.ErrTooManyRows.Error())
	}
	rows := make([]inference.Row, len(req.GetInstances()))
	for i, in := range req.GetInstances() {
		rows[i] = in.AsMap()
	}

	preds, err := svc.Predict(ml.WithCaller(ctx, storage.MLCallerGRPC, ""), req.GetVhost(), req.GetModel(), rows)
	if err != nil {
		if errors.Is(err, ml.ErrTooManyRows) {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		if errors.Is(err, ml.ErrQuotaExceeded) {
			return nil, status.Error(codes.ResourceExhausted, err.Error())
		}
		return nil, status.Error(codes.Unavailable, err.Error())
	}

	resp := &proto.PredictResponse{Model: req.GetModel(), Predictions: make([]*structpb.Struct, len(preds))}
	for i, p := range preds {
		st, err := structpb.NewStruct(p)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "prediction %d cannot be sent: %v", i, err)
		}
		resp.Predictions[i] = st
	}
	return resp, nil
}

// apiKey is the "x-api-key" metadata, as the gRPC source reads it.
func apiKey(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	if v := md.Get("x-api-key"); len(v) > 0 {
		return v[0]
	}
	return ""
}
