package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func (s *Service) deliverAggregate(ctx context.Context, now time.Time) (bool, error) {
	started := time.Now()
	g, err := s.store.ClaimAggregate(ctx, now, time.Minute)
	if err != nil || g == nil {
		return false, err
	}
	send, err := s.store.PrepareAggregateSend(ctx, g.ID, g.LeaseToken, now.Add(time.Since(started)))
	if errors.Is(err, store.ErrDeliveryCanceled) || errors.Is(err, store.ErrAggregatePreparationFailed) {
		return true, nil
	}
	if err != nil {
		return true, err
	}
	channel := send.Channel
	config, configErr := s.decryptConfig(channel)
	var notification Notification
	var deliveryErr error
	if configErr != nil {
		deliveryErr = &DeliveryError{Class: "channel_configuration", Permanent: true}
	} else if err := json.Unmarshal(send.Payload, &notification); err != nil {
		deliveryErr = &DeliveryError{Class: "invalid_payload", Permanent: true}
	}
	if deliveryErr == nil {
		notification.IdempotencyKey = send.IdempotencyKey
		notification.templateEventTime = send.TemplateEventTime
		sendCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		deliveryErr = s.sender.Deliver(sendCtx, channel.Kind, config, notification)
		cancel()
	}
	finished := now.Add(time.Since(started))
	return true, s.store.CompleteAggregate(ctx, g.ID, g.LeaseToken, finished, deliveryOutcome(deliveryErr, send.MaxMemberAttempt, finished))
}
