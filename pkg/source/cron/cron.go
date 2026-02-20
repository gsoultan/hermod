package cron

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/robfig/cron/v3"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

// CronSource implements the hermod.Source interface for scheduled triggers.
type CronSource struct {
	Schedule string
	Payload  string
	cron     *cron.Cron
	ch       chan hermod.Message
	entryID  cron.EntryID
}

// NewCronSource creates a new CronSource.
func NewCronSource(schedule, payload string) *CronSource {
	return &CronSource{
		Schedule: schedule,
		Payload:  payload,
		cron:     cron.New(),
		ch:       make(chan hermod.Message, 10),
	}
}

// Read blocks until the cron trigger fires.
func (s *CronSource) Read(ctx context.Context) (hermod.Message, error) {
	if s.entryID == 0 {
		var err error
		s.entryID, err = s.cron.AddFunc(s.Schedule, func() {
			msg := message.AcquireMessage()
			msg.SetID(fmt.Sprintf("cron_%d", s.entryID))
			msg.SetOperation(hermod.OpSnapshot)
			msg.SetTable("cron")

			if s.Payload != "" {
				var data map[string]any
				if err := json.Unmarshal([]byte(s.Payload), &data); err == nil {
					msg.SetAfter([]byte(s.Payload))
				}
			}

			select {
			case s.ch <- msg:
			default:
				message.ReleaseMessage(msg)
			}
		})
		if err != nil {
			return nil, fmt.Errorf("failed to schedule cron: %w", err)
		}
		s.cron.Start()
	}

	select {
	case msg := <-s.ch:
		return msg, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Ack is a no-op for cron.
func (s *CronSource) Ack(ctx context.Context, msg hermod.Message) error { return nil }

// Ping checks if the schedule is valid.
func (s *CronSource) Ping(ctx context.Context) error {
	_, err := cron.ParseStandard(s.Schedule)
	if err != nil {
		// Try Parser with v3 options
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
		_, err = parser.Parse(s.Schedule)
	}
	return err
}

// Close stops the cron scheduler.
func (s *CronSource) Close() error {
	s.cron.Stop()
	return nil
}
