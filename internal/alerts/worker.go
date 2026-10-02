package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func (s *Service) runDeliveryWorker(ctx context.Context) {
	for ctx.Err() == nil {
		worked, err := s.DeliverOne(ctx, time.Now().UTC())
		if err != nil && ctx.Err() == nil {
			s.logger.Warn("notification queue operation failed")
		}
		if worked && err == nil {
			continue
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// DeliverOne performs network I/O outside database transactions. An ambiguous
// completion remains leased until recovery; delivery is at least once.
func (s *Service) DeliverOne(ctx context.Context, now time.Time) (bool, error) {
	started := time.Now()
	job, err := s.store.ClaimDelivery(ctx, now, time.Minute)
	if err != nil || job == nil {
		return false, err
	}
	channel, err := s.store.NotificationChannel(ctx, job.ChannelID)
	var config ChannelConfig
	var notification Notification
	var deliveryErr error
	if err != nil {
		return true, err
	}
	if config, err = s.decryptConfig(channel); err != nil {
		deliveryErr = &DeliveryError{Class: "channel_configuration", Permanent: true}
	} else if err = json.Unmarshal(job.Payload, &notification); err != nil {
		deliveryErr = &DeliveryError{Class: "invalid_payload", Permanent: true}
	}
	if err := s.store.CheckDeliveryLease(ctx, job.ID, job.LeaseToken, now.Add(time.Since(started))); err != nil {
		if errors.Is(err, store.ErrDeliveryCanceled) || errors.Is(err, store.ErrDeliveryDeferred) {
			return true, nil
		}
		return true, err
	}
	if deliveryErr == nil {
		notification.IdempotencyKey = job.IdempotencyKey
		sendCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		deliveryErr = s.sender.Deliver(sendCtx, channel.Kind, config, notification)
		cancel()
	}
	finished := now.Add(time.Since(started))
	outcome := store.DeliveryOutcome{Delivered: deliveryErr == nil}
	if deliveryErr != nil {
		outcome.ErrorClass = "delivery_failed"
		delay := retryDelay(job.AttemptCount)
		var classified *DeliveryError
		if errors.As(deliveryErr, &classified) {
			outcome.ErrorClass, outcome.Permanent = classified.Class, classified.Permanent
			outcome.Ambiguous = classified.Ambiguous
			if classified.RetryAfter > delay {
				delay = classified.RetryAfter
			}
		}
		outcome.RetryAt = finished.Add(delay)
	}
	return true, s.store.CompleteDelivery(ctx, job.ID, job.LeaseToken, finished, outcome)
}

func retryDelay(attempt int) time.Duration {
	delays := [...]time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, 30 * time.Minute}
	if attempt < 1 {
		attempt = 1
	}
	if attempt > len(delays) {
		attempt = len(delays)
	}
	base := delays[attempt-1]
	return base + time.Duration(rand.Int64N(int64(base/5)+1))
}
