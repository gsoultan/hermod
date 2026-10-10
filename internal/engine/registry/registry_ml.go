package registry

import (
	"context"

	"github.com/gsoultan/hermod/internal/ml"
	"github.com/gsoultan/hermod/pkg/ml/inference"
)

// MLService is the model registry and inference path over whatever storage
// and secrets the registry holds right now. It is cheap to build, so it is
// built per use rather than cached against a store that may be replaced.
func (r *Registry) MLService() *ml.Service {
	return ml.NewService(func() any { return r.store() }, r.vhostSecrets(), nil)
}

// MLPredict is what the Predict transformer calls: the vhost's model, with
// the rows it was given.
func (r *Registry) MLPredict(ctx context.Context, vhost, model string, rows []inference.Row) ([]inference.Row, error) {
	return r.MLService().Predict(ctx, vhost, model, rows)
}
