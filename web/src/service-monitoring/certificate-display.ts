export const certificateEndReasons: Record<string, string> = {
  certificate_expired: '证书已到期，提醒档已结束',
  certificate_threshold_advanced: '已进入更紧急提醒档',
  certificate_no_longer_observed: '完整的新鲜成功检查未再观测到该证书',
}
export const certificateDeliveryLabels: Record<string, string> = {
  not_queued: '尚未入队', pending: '等待发送', inflight: '发送中', delivered: '已送达',
  failed: '发送失败', canceled: '任务已取消', history_unavailable: '投递历史已不可用',
}
