package grafana_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestInternetScaleDashboardCoversCapacityAndCostSignals(t *testing.T) {
	raw, err := os.ReadFile("budgie-internet-scale-dashboard.json")
	if err != nil {
		t.Fatalf("read dashboard: %v", err)
	}
	var dashboard struct {
		Title  string `json:"title"`
		Panels []struct {
			Title   string `json:"title"`
			Targets []struct {
				Expr string `json:"expr"`
			} `json:"targets"`
		} `json:"panels"`
	}
	if err := json.Unmarshal(raw, &dashboard); err != nil {
		t.Fatalf("decode dashboard: %v", err)
	}
	if dashboard.Title != "Budgie Internet Scale Capacity And Cost" {
		t.Fatalf("dashboard title = %q", dashboard.Title)
	}
	if len(dashboard.Panels) != 12 {
		t.Fatalf("panel count = %d, want 12", len(dashboard.Panels))
	}

	body := string(raw)
	for _, title := range []string{
		"Live Delivery SLO",
		"Gateway Fanout Capacity",
		"Gateway Socket Cost",
		"Writer Partition Capacity",
		"Hot Partition Candidates",
		"Writer Assignment And Receipt Health",
		"Command Latency And Lock Wait",
		"Derived View Freshness",
		"Broker Egress And Replay Cost",
		"Regional Write Routing Cost",
		"Event Log Shadow Promotion Gate",
		"Worker Repair Backlog",
	} {
		if !strings.Contains(body, `"title": "`+title+`"`) {
			t.Fatalf("missing dashboard panel %q", title)
		}
	}
	for _, metric := range []string{
		"budgie_remote_wakeup_lag_ms_bucket",
		"budgie_gateway_connection_queue_depth",
		"budgie_gateway_connection_queue_capacity",
		"budgie_gateway_dropped_sends_total",
		"budgie_gateway_replay_repairs_total",
		"budgie_ws_connections",
		"budgie_ssh_sessions",
		"budgie_command_partition_lag",
		"budgie_command_partition_lag_skew",
		"budgie_hot_partition_candidate",
		"budgie_command_partition_assigned_count",
		"budgie_command_log_assignment_losses_total",
		"budgie_command_partition_assignment_generation",
		"budgie_command_log_receipts",
		"budgie_command_log_receipt_oldest_age_ms",
		"budgie_command_latency_ms_bucket",
		"budgie_writer_lock_wait_ms_bucket",
		"budgie_scalar_append_gate_wait_ms_bucket",
		"budgie_derived_view_lag_events",
		"budgie_derived_view_applied_seq",
		"budgie_events_published_remote_total",
		"budgie_events_ingested_remote_total",
		"budgie_events_remote_publish_failures_total",
		"budgie_replay_total",
		"budgie_write_region_routed_requests_total",
		"budgie_write_region_proxy_failures_total",
		"budgie_event_log_shadow_append_failures_total",
		"budgie_event_log_shadow_parity_failures_total",
		"budgie_outbox_jobs",
		"budgie_worker_is_leader",
	} {
		if !strings.Contains(body, metric) {
			t.Fatalf("dashboard missing metric %q", metric)
		}
	}
	if !strings.Contains(body, `"capacity"`) || !strings.Contains(body, `"cost"`) {
		t.Fatal("dashboard must be tagged for capacity and cost")
	}
}
