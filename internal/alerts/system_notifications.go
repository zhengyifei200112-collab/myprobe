package alerts

import (
	"context"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func (s *Service) runSystemNotificationWorker(ctx context.Context) {
	for ctx.Err() == nil {
		worked, err := s.DeliverSystemNotification(ctx, time.Now().UTC())
		if err != nil && ctx.Err() == nil {
			s.logger.Warn("system notification queue operation failed")
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

// System failures retain their own durable identity, without creating a fake
// monitored node. Providers, encryption, retry classification and channel pauses
// are the same implementation as incident delivery.
func (s *Service) DeliverSystemNotification(ctx context.Context, now time.Time) (bool, error) {
	started := time.Now()
	job, err := s.store.ClaimSystemNotification(ctx, now)
	if err != nil || job == nil {
		return false, err
	}
	channel, err := s.store.NotificationChannel(ctx, job.ChannelID)
	if err != nil {
		return true, err
	}
	config, configErr := s.decryptConfig(channel)
	allowed, err := s.store.AuthorizeSystemNotification(ctx, job.ID, job.LeaseToken, channel.DeliveryRevision, now.Add(time.Since(started)))
	if err != nil || !allowed {
		return true, err
	}
	var deliveryErr error
	if configErr != nil {
		deliveryErr = &DeliveryError{Class: "channel_configuration", Permanent: true}
	} else {
		n := Notification{Title: "MyProbe 备份失败", Message: "数据库备份未完成。错误分类：" + job.ErrorCode + "。请在管理面板的备份任务中查看记录。", State: "failed", Kind: "backup_failure", Timestamp: job.OccurredAt, IdempotencyKey: "system-backup-" + job.ID}
		sendCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		deliveryErr = s.sender.Deliver(sendCtx, job.Provider, config, n)
		cancel()
	}
	finished := now.Add(time.Since(started))
	outcome := deliveryOutcome(deliveryErr, job.AttemptCount, finished)
	outcome.ChannelRevision = job.ChannelRevision
	return true, s.store.CompleteSystemNotification(ctx, job.ID, job.LeaseToken, finished, outcome)
}

// VerifyRecoveryChannels checks that the separately retained application key
// can decrypt every recovered channel. It performs no external delivery.
func (s *Service) VerifyRecoveryChannels(ctx context.Context, recovered *store.Store) error {
	channels, err := recovered.ListNotificationChannels(ctx)
	if err != nil {
		return err
	}
	for _, channel := range channels {
		if _, err = s.decryptConfig(channel); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) ValidateBackupFailureChannels(ctx context.Context, ids []string) error {
	for _, id := range ids {
		channel, err := s.store.NotificationChannel(ctx, id)
		if err != nil || !channel.Enabled {
			return store.ErrNotFound
		}
		if _, err = s.decryptConfig(channel); err != nil {
			return err
		}
	}
	return nil
}
