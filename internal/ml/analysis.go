package ml

import (
	"context"
	"errors"

	"github.com/gsoultan/hermod/pkg/ml/profile"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// ErrAnalysisUnsupported is a dataset kept on a hermod-ml worker older than
// dataset analysis, which cannot give a sample of it.
var ErrAnalysisUnsupported = errors.New("the hermod-ml worker that holds this dataset is older than dataset analysis: update it to analyze the dataset")

// ProfileDataset describes a vhost's dataset (pkg/ml/profile), computed on at
// most worker.SampleMaxRows of its rows spread evenly over it, from whichever
// engine holds it.
func (s *Service) ProfileDataset(ctx context.Context, vhost, name string) (profile.Profile, error) {
	w, err := s.Worker()
	if err != nil {
		return profile.Profile{}, err
	}
	sample, err := w.DatasetSample(ctx, vhost, name, worker.SampleMaxRows)
	if errors.Is(err, worker.ErrNotFound) {
		// A worker without the sample route answers not found for every
		// dataset; one that has the dataset is too old, not missing it.
		if _, derr := w.Dataset(ctx, vhost, name); derr == nil {
			return profile.Profile{}, ErrAnalysisUnsupported
		}
	}
	if err != nil {
		return profile.Profile{}, err
	}
	return profile.Compute(sample, s.now()), nil
}
