// Package grpc serves hermod.ml.v1 InferenceService: the gRPC twin of
// POST /api/ml/serve/{vhost}/{name}.
package grpc

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/gsoultan/hermod/internal/ml"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/inference"
	"github.com/gsoultan/hermod/pkg/ml/proto"
	"github.com/gsoultan/hermod/pkg/ml/recommend"
)

// Server implements proto.InferenceServiceServer.
type Server struct {
	proto.UnimplementedInferenceServiceServer
	// ServiceFunc returns the ML service to use, looked up per call because
	// first-run setup and a database switch replace the store while the gRPC
	// server keeps running.
	ServiceFunc func() *ml.Service
	// watchEvery is how often WatchJob reads its job; zero is
	// defaultWatchEvery. Tests shorten it.
	watchEvery time.Duration
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

	var preds []inference.Row
	err := svc.Sync(ml.WithCaller(ctx, storage.MLCallerGRPC, ""), req.GetVhost(), req.GetModel(), func(ctx context.Context) error {
		var err error
		preds, err = svc.Predict(ctx, req.GetVhost(), req.GetModel(), rows)
		return err
	})
	if err != nil {
		if errors.Is(err, ml.ErrSyncTimeout) {
			return nil, status.Error(codes.DeadlineExceeded, syncHint(err))
		}
		if errors.Is(err, ml.ErrTooManyRows) || errors.Is(err, ml.ErrRecommender) {
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

// Recommend authenticates the caller as Predict does, then asks the
// recommendation model for a person's items or the items like one item.
func (s *Server) Recommend(ctx context.Context, req *proto.RecommendRequest) (*proto.RecommendResponse, error) {
	svc := s.ServiceFunc()
	if svc == nil {
		return nil, status.Error(codes.Unavailable, "Hermod is not set up yet")
	}
	if err := svc.AuthorizeServing(ctx, req.GetVhost(), req.GetModel(), apiKey(ctx)); err != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid serving key")
	}

	call := ml.RecommendRequest{K: int(req.GetK()), ExcludeSeen: req.ExcludeSeen}
	switch subject := req.GetSubject().(type) {
	case *proto.RecommendRequest_User:
		call.User = subject.User
	case *proto.RecommendRequest_Item:
		call.Item = subject.Item
	}
	var items []recommend.Item
	err := svc.Sync(ml.WithCaller(ctx, storage.MLCallerGRPC, ""), req.GetVhost(), req.GetModel(), func(ctx context.Context) error {
		var err error
		items, err = svc.Recommend(ctx, req.GetVhost(), req.GetModel(), call)
		return err
	})
	if err != nil {
		switch {
		case errors.Is(err, ml.ErrSyncTimeout):
			return nil, status.Error(codes.DeadlineExceeded, syncHint(err))
		case errors.Is(err, ml.ErrBadRecommendRequest), errors.Is(err, ml.ErrNotRecommender):
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case errors.Is(err, ml.ErrQuotaExceeded):
			return nil, status.Error(codes.ResourceExhausted, err.Error())
		}
		return nil, status.Error(codes.Unavailable, err.Error())
	}

	resp := &proto.RecommendResponse{Model: req.GetModel(), Items: make([]*proto.RecommendedItem, len(items))}
	for i, it := range items {
		resp.Items[i] = &proto.RecommendedItem{Item: it.Item, Score: it.Score, Reason: it.Reason}
	}
	return resp, nil
}

// syncHint is a sync time limit refusal for a gRPC caller, who sends the
// call as a job with SubmitJob rather than with "respond".
func syncHint(err error) string {
	var limit *ml.SyncTimeoutError
	if errors.As(err, &limit) {
		return "the model did not answer within " + limit.Limit.String() +
			", the limit for a call that waits for its answer; send it with SubmitJob and read the answer with GetJob or WatchJob"
	}
	return err.Error()
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
