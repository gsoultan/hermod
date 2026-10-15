// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package grpc

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/gsoultan/hermod/internal/ml"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/inference"
	"github.com/gsoultan/hermod/pkg/ml/proto"
	"github.com/gsoultan/hermod/pkg/ml/recommend"
)

// defaultWatchEvery is how often WatchJob reads the job it follows.
const defaultWatchEvery = time.Second

// SubmitJob queues a Predict or Recommend call as a job, authenticated with
// the serving key of the call's model, as the call itself would be.
func (s *Server) SubmitJob(ctx context.Context, req *proto.SubmitJobRequest) (*proto.SubmitJobResponse, error) {
	svc := s.ServiceFunc()
	if svc == nil {
		return nil, status.Error(codes.Unavailable, "Hermod is not set up yet")
	}
	sub := ml.JobSubmission{CallbackURL: req.GetCallbackUrl(), CallerKind: storage.MLCallerGRPC}
	switch call := req.GetCall().(type) {
	case *proto.SubmitJobRequest_Predict:
		p := call.Predict
		sub.VHost, sub.Model, sub.Kind = p.GetVhost(), p.GetModel(), storage.MLJobPredict
		sub.Rows = make([]inference.Row, len(p.GetInstances()))
		for i, in := range p.GetInstances() {
			sub.Rows[i] = in.AsMap()
		}
	case *proto.SubmitJobRequest_Recommend:
		r := call.Recommend
		sub.VHost, sub.Model, sub.Kind = r.GetVhost(), r.GetModel(), storage.MLJobRecommend
		rec := ml.RecommendRequest{K: int(r.GetK()), ExcludeSeen: r.ExcludeSeen}
		switch subject := r.GetSubject().(type) {
		case *proto.RecommendRequest_User:
			rec.User = subject.User
		case *proto.RecommendRequest_Item:
			rec.Item = subject.Item
		}
		sub.Recommend = &rec
	default:
		return nil, status.Error(codes.InvalidArgument, "name the call to run: predict or recommend")
	}
	if err := svc.AuthorizeServing(ctx, sub.VHost, sub.Model, apiKey(ctx)); err != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid serving key")
	}
	j, err := svc.SubmitJob(ctx, sub)
	if err != nil {
		return nil, jobError(err)
	}
	pj, err := jobOf(ctx, svc, j)
	if err != nil {
		return nil, err
	}
	return &proto.SubmitJobResponse{Job: pj}, nil
}

// GetJob returns a job submitted with the serving key presented.
func (s *Server) GetJob(ctx context.Context, req *proto.GetJobRequest) (*proto.GetJobResponse, error) {
	svc, j, err := s.servingJob(ctx, req.GetVhost(), req.GetJobId())
	if err != nil {
		return nil, err
	}
	pj, err := jobOf(ctx, svc, j)
	if err != nil {
		return nil, err
	}
	return &proto.GetJobResponse{Job: pj}, nil
}

// CancelJob cancels a job submitted with the serving key presented.
func (s *Server) CancelJob(ctx context.Context, req *proto.CancelJobRequest) (*proto.CancelJobResponse, error) {
	svc, j, err := s.servingJob(ctx, req.GetVhost(), req.GetJobId())
	if err != nil {
		return nil, err
	}
	j, err = svc.CancelJob(ctx, j.VHost, j.ID)
	if err != nil {
		return nil, jobError(err)
	}
	pj, err := jobOf(ctx, svc, j)
	if err != nil {
		return nil, err
	}
	return &proto.CancelJobResponse{Job: pj}, nil
}

// WatchJob sends the job, then again whenever its status or progress
// changes, until it has sent it finished.
func (s *Server) WatchJob(req *proto.WatchJobRequest, stream grpc.ServerStreamingServer[proto.WatchJobResponse]) error {
	ctx := stream.Context()
	svc, j, err := s.servingJob(ctx, req.GetVhost(), req.GetJobId())
	if err != nil {
		return err
	}
	every := s.watchEvery
	if every <= 0 {
		every = defaultWatchEvery
	}
	t := time.NewTicker(every)
	defer t.Stop()
	var sent *storage.MLJob
	for {
		if sent == nil || j.Status != sent.Status || j.Progress != sent.Progress || j.ProgressMessage != sent.ProgressMessage ||
			j.CancelRequested != sent.CancelRequested {
			pj, err := jobOf(ctx, svc, j)
			if err != nil {
				return err
			}
			if err := stream.Send(&proto.WatchJobResponse{Job: pj}); err != nil {
				return err
			}
			last := j
			sent = &last
		}
		if storage.MLJobTerminal(j.Status) {
			return nil
		}
		select {
		case <-ctx.Done():
			return status.FromContextError(ctx.Err()).Err()
		case <-t.C:
		}
		if j, err = svc.Job(ctx, j.VHost, j.ID); err != nil {
			return jobError(err)
		}
	}
}

// servingJob is the job asked about, when the key presented may read it:
// one submitted with a serving key, for the model the key is for. Every
// refusal but a missing key is the same NotFound, so a key does not learn
// of jobs that are not its own.
func (s *Server) servingJob(ctx context.Context, vhost, id string) (*ml.Service, storage.MLJob, error) {
	svc := s.ServiceFunc()
	if svc == nil {
		return nil, storage.MLJob{}, status.Error(codes.Unavailable, "Hermod is not set up yet")
	}
	key := apiKey(ctx)
	if key == "" {
		return nil, storage.MLJob{}, status.Error(codes.Unauthenticated, "send the model's serving key as x-api-key")
	}
	j, err := svc.Job(ctx, vhost, id)
	if err != nil || !ml.ServingJob(j, j.Model) || svc.AuthorizeServing(ctx, vhost, j.Model, key) != nil {
		return nil, storage.MLJob{}, status.Error(codes.NotFound, "no such job")
	}
	return svc, j, nil
}

// jobError maps a job error onto a status.
func jobError(err error) error {
	switch {
	case errors.Is(err, ml.ErrNoCallbackSecret):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, ml.ErrBadJob), errors.Is(err, ml.ErrTooManyRows), errors.Is(err, ml.ErrRecommender),
		errors.Is(err, ml.ErrNotRecommender), errors.Is(err, ml.ErrBadRecommendRequest):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, ml.ErrQuotaExceeded):
		return status.Error(codes.ResourceExhausted, err.Error())
	case errors.Is(err, ml.ErrJobNotFound), errors.Is(err, ml.ErrModelNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, storage.ErrMLJobsUnsupported):
		return status.Error(codes.Unimplemented, err.Error())
	}
	return status.Error(codes.Unavailable, err.Error())
}

// jobOf is a job as the wire carries it, with its result once it has one.
func jobOf(ctx context.Context, svc *ml.Service, j storage.MLJob) (*proto.Job, error) {
	pj := &proto.Job{
		Id: j.ID, Vhost: j.VHost, Model: j.Model, Kind: j.Kind, Status: j.Status, Progress: j.Progress,
		ProgressMessage: j.ProgressMessage, Error: j.Error, CancelRequested: j.CancelRequested,
		CreatedAt: timestamppb.New(j.CreatedAt), ExpiresAt: timestamppb.New(j.ExpiresAt),
	}
	if j.StartedAt != nil {
		pj.StartedAt = timestamppb.New(*j.StartedAt)
	}
	if j.FinishedAt != nil {
		pj.FinishedAt = timestamppb.New(*j.FinishedAt)
	}
	if j.Status != storage.MLJobSucceeded {
		return pj, nil
	}
	raw := j.Result
	if j.ResultRef == storage.MLJobResultStored {
		var err error
		if raw, err = svc.JobResult(ctx, j.VHost, j.ID); err != nil {
			return nil, jobError(err)
		}
	}
	return pj, setResult(pj, raw)
}

// setResult puts a succeeded job's result on pj, as the call it stands for
// would have answered.
func setResult(pj *proto.Job, raw []byte) error {
	switch pj.GetKind() {
	case storage.MLJobPredict:
		var res struct {
			Predictions []map[string]any `json:"predictions"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return status.Errorf(codes.Internal, "reading the job's result: %v", err)
		}
		out := &proto.PredictResponse{Model: pj.GetModel(), Predictions: make([]*structpb.Struct, len(res.Predictions))}
		for i, p := range res.Predictions {
			st, err := structpb.NewStruct(p)
			if err != nil {
				return status.Errorf(codes.Internal, "prediction %d cannot be sent: %v", i, err)
			}
			out.Predictions[i] = st
		}
		pj.Result = &proto.Job_PredictResult{PredictResult: out}
	case storage.MLJobRecommend:
		// A scenario call's answer holds "results"; a call for a user or an
		// item's, "items".
		var res struct {
			Items            []recommend.Item   `json:"items"`
			RecommendationID string             `json:"recommendation_id"`
			Results          []recommend.Result `json:"results"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return status.Errorf(codes.Internal, "reading the job's result: %v", err)
		}
		if res.Results != nil {
			out, err := scenarioResponse(pj.GetModel(), ml.ScenarioAnswer{RecommendationID: res.RecommendationID, Results: res.Results})
			if err != nil {
				return err
			}
			pj.Result = &proto.Job_RecommendScenarioResult{RecommendScenarioResult: out}
			return nil
		}
		out := &proto.RecommendResponse{Model: pj.GetModel(), RecommendationId: res.RecommendationID,
			Items: make([]*proto.RecommendedItem, len(res.Items))}
		for i, it := range res.Items {
			out.Items[i] = &proto.RecommendedItem{Item: it.Item, Score: it.Score, Reason: it.Reason}
		}
		pj.Result = &proto.Job_RecommendResult{RecommendResult: out}
	}
	return nil
}
