package scheduler

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"w8nc/internal/conditions"
	"w8nc/internal/db"
	"w8nc/internal/models"
	"w8nc/internal/notifier"
	"w8nc/internal/pinger"
)

type Service struct {
	Store       *db.Store
	Pinger      *pinger.Pinger
	Notifier    *notifier.Notifier
	Tick        time.Duration
	Concurrency int
	LockFor     time.Duration
	Logger      *slog.Logger
}

func (s *Service) Start(ctx context.Context) {
	go s.scheduleLoop(ctx)
	go s.notificationLoop(ctx)
}

func (s *Service) PingNow(ctx context.Context, endpoint models.Endpoint) error {
	_, err := s.processEndpoint(ctx, endpoint, endpoint.Active, true)
	return err
}

func (s *Service) RunDueOnce(ctx context.Context) error {
	limit := s.Concurrency
	if limit <= 0 {
		limit = 1
	}
	endpoints, err := s.Store.LockDueEndpoints(ctx, limit, s.LockFor)
	if err != nil {
		return err
	}
	var wg sync.WaitGroup
	jobs := make(chan models.Endpoint)
	workers := s.Concurrency
	if workers <= 0 {
		workers = 1
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for endpoint := range jobs {
				if _, err := s.processEndpoint(ctx, endpoint, true, false); err != nil && s.Logger != nil {
					s.Logger.Error("endpoint ping failed", "endpoint_id", endpoint.ID, "error", err)
				}
			}
		}()
	}
	for _, endpoint := range endpoints {
		select {
		case jobs <- endpoint:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return ctx.Err()
		}
	}
	close(jobs)
	wg.Wait()
	return nil
}

func (s *Service) DispatchNotificationsOnce(ctx context.Context) error {
	settings, err := s.Store.GetNotificationSettings(ctx)
	if err != nil {
		return err
	}
	events, err := s.Store.DueNotificationEvents(ctx, 20)
	if err != nil {
		return err
	}
	for _, event := range events {
		if err := s.Notifier.Send(ctx, settings, event.Message); err != nil {
			if markErr := s.Store.MarkNotificationFailed(ctx, event.ID, err.Error()); markErr != nil && s.Logger != nil {
				s.Logger.Error("notification failure could not be recorded", "event_id", event.ID, "error", markErr)
			}
			continue
		}
		if err := s.Store.MarkNotificationSent(ctx, event.ID); err != nil && s.Logger != nil {
			s.Logger.Error("notification sent state could not be recorded", "event_id", event.ID, "error", err)
		}
	}
	return nil
}

func (s *Service) processEndpoint(ctx context.Context, endpoint models.Endpoint, evaluate bool, manual bool) (string, error) {
	result := s.Pinger.Ping(ctx, endpoint)
	evaluation := conditions.Evaluation{
		ConditionType:  endpoint.NotifyCondition.Type,
		BaselineStatus: endpoint.BaselineStatusCode,
		BaselineLength: endpoint.BaselineResponseLength,
	}
	conditionMatched := false
	if evaluate {
		nextEvaluation, err := conditions.Evaluate(endpoint, result)
		if err != nil {
			message := err.Error()
			result.Error = &message
		} else {
			evaluation = nextEvaluation
			conditionMatched = nextEvaluation.Matched
		}
	} else {
		if result.StatusCode != nil {
			status := *result.StatusCode
			evaluation.BaselineStatus = &status
		}
		if result.ResponseLength != nil && !result.Truncated {
			length := *result.ResponseLength
			evaluation.BaselineLength = &length
		}
	}

	message := ""
	if conditionMatched {
		settings, err := s.Store.GetNotificationSettings(ctx)
		if err != nil {
			return "", err
		}
		message = conditions.RenderTemplate(endpoint.NotificationTemplate, endpoint, result, evaluation, settings.Timezone)
	}
	var nextRun *time.Time
	if endpoint.Active && (!conditionMatched || !endpoint.NotifyOnce) {
		value := result.FinishedAt.Add(time.Duration(endpoint.PingIntervalSeconds) * time.Second)
		nextRun = &value
	}
	return s.Store.RecordPingResult(ctx, endpoint, result, conditionMatched, message, nextRun, evaluation.BaselineStatus, evaluation.BaselineLength, manual)
}

func (s *Service) scheduleLoop(ctx context.Context) {
	ticker := time.NewTicker(s.Tick)
	defer ticker.Stop()
	for {
		if err := s.RunDueOnce(ctx); err != nil && s.Logger != nil && ctx.Err() == nil {
			s.Logger.Error("scheduler tick failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) notificationLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		if err := s.DispatchNotificationsOnce(ctx); err != nil && s.Logger != nil && ctx.Err() == nil {
			s.Logger.Error("notification dispatch failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
