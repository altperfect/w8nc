package screenshots

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"w8nc/internal/db"
	"w8nc/internal/notifier"
)

type Service struct {
	Store       *db.Store
	Capturer    Capturer
	Notifier    *notifier.Notifier
	Tick        time.Duration
	Concurrency int
	Logger      *slog.Logger
}

func (s *Service) Start(ctx context.Context) {
	go s.loop(ctx)
}

func (s *Service) RunOnce(ctx context.Context) error {
	limit := s.Concurrency
	if limit <= 0 {
		limit = 1
	}
	attempts, err := s.Store.DueScreenshotAttempts(ctx, limit)
	if err != nil {
		return err
	}
	for _, attempt := range attempts {
		if err := s.process(ctx, attempt.ID, attempt.EndpointID); err != nil && s.Logger != nil {
			s.Logger.Error("screenshot attempt failed", "attempt_id", attempt.ID, "endpoint_id", attempt.EndpointID, "error", err)
		}
	}
	return nil
}

func (s *Service) process(ctx context.Context, attemptID string, endpointID string) error {
	claimed, err := s.Store.MarkScreenshotCapturing(ctx, attemptID)
	if err != nil {
		return err
	}
	if !claimed {
		return nil
	}
	endpoint, err := s.Store.GetEndpoint(ctx, endpointID)
	if err != nil {
		message := "endpoint could not be loaded"
		_ = s.Store.MarkScreenshotFailed(ctx, attemptID, "", "", 0, message)
		return fmt.Errorf("%s: %w", message, err)
	}
	result, err := s.Capturer.Capture(ctx, attemptID, endpoint)
	if err != nil {
		if IsUnsupported(err) {
			_ = s.Store.MarkScreenshotUnsupported(ctx, attemptID, err.Error())
			return nil
		}
		_ = s.Store.MarkScreenshotFailed(ctx, attemptID, "", "", 0, err.Error())
		return err
	}
	settings, err := s.Store.GetNotificationSettings(ctx)
	if err != nil {
		_ = s.Store.MarkScreenshotFailed(ctx, attemptID, result.Path, result.ContentType, result.Size, "notification settings could not be loaded")
		return err
	}
	if err := s.Notifier.SendScreenshot(ctx, settings, endpoint.URL, result.Path); err != nil {
		_ = s.Store.MarkScreenshotFailed(ctx, attemptID, result.Path, result.ContentType, result.Size, err.Error())
		return err
	}
	return s.Store.MarkScreenshotSucceeded(ctx, attemptID, result.Path, result.ContentType, result.Size)
}

func (s *Service) loop(ctx context.Context) {
	tick := s.Tick
	if tick <= 0 {
		tick = 5 * time.Second
	}
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		if err := s.RunOnce(ctx); err != nil && s.Logger != nil && ctx.Err() == nil {
			s.Logger.Error("screenshot worker tick failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
