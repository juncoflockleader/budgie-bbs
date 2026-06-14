package prometheus_test

import (
	"os"
	"strings"
	"testing"
)

func TestInternetScaleAlertRulesCoverIS9Signals(t *testing.T) {
	raw, err := os.ReadFile("budgie-internet-scale-alerts.yml")
	if err != nil {
		t.Fatalf("read alert rules: %v", err)
	}
	body := string(raw)
	for _, want := range []string{
		"BudgieRemoteDeliveryLagHigh",
		"BudgieGatewayQueueSaturation",
		"BudgieGatewayDropsWithoutReplayRepair",
		"BudgieProjectionLagHigh",
		"BudgieCommandLogWriterLagHigh",
		"BudgieCommandAssignmentLosses",
		"BudgieCommandLogRetryingReceiptsStuck",
		"BudgieAttachmentBlobStagingExpired",
		"BudgieWriteRegionProxyFailures",
		"BudgieCommandLatencyHigh",
		"BudgieWriterLockWaitHigh",
		"BudgieOutboxDeadJobs",
		"BudgieEventLogShadowParityFailure",
		"BudgieHotPartitionCandidate",
	} {
		if !strings.Contains(body, "alert: "+want) {
			t.Fatalf("missing alert %s", want)
		}
	}
	for _, metric := range []string{
		"budgie_remote_wakeup_lag_ms_bucket",
		"budgie_gateway_connection_queue_depth",
		"budgie_gateway_connection_queue_capacity",
		"budgie_gateway_dropped_sends_total",
		"budgie_gateway_replay_repairs_total",
		"budgie_derived_view_lag_events",
		"budgie_command_partition_lag",
		"budgie_command_log_assignment_losses_total",
		"budgie_command_log_receipts",
		"budgie_command_log_receipt_oldest_age_ms",
		"budgie_attachment_blob_staging_blobs",
		"budgie_write_region_proxy_failures_total",
		"budgie_command_latency_ms_bucket",
		"budgie_writer_lock_wait_ms_bucket",
		"budgie_outbox_jobs",
		"budgie_event_log_shadow_parity_failures_total",
		"budgie_hot_partition_candidate",
	} {
		if !strings.Contains(body, metric) {
			t.Fatalf("missing metric %s", metric)
		}
	}
	if strings.Contains(body, "\t") {
		t.Fatal("alert rules must not contain tabs")
	}
	if got := strings.Count(body, "      - alert: "); got != 14 {
		t.Fatalf("alert count = %d, want 14", got)
	}
}
