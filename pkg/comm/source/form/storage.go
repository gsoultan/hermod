package form

import (
	"context"
	"time"
)

type FormSubmission struct {
	ID        string
	Timestamp time.Time
	Path      string
	Data      []byte
	Status    string
}

type FormSubmissionFilter struct {
	Path   string
	Status string
	Limit  int
	Page   int
}

type Storage interface {
	CreateFormSubmission(ctx context.Context, sub FormSubmission) error
	ListFormSubmissions(ctx context.Context, filter FormSubmissionFilter) ([]FormSubmission, int, error)
	UpdateFormSubmissionStatus(ctx context.Context, id string, status string) error
}
