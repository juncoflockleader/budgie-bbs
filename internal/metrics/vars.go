package metrics

// Process-wide metric handles for the multi-node and internet-scale
// observability surfaces. Counters/gauges are safe to touch from any goroutine;
// histograms record in milliseconds (latency/lag) or raw counts (batch size).
var (
	// Connection gauges.
	WSConnections    = NewGauge("budgie_ws_connections", "Open WebSocket connections on this node.")
	LocalSubscribers = NewGauge("budgie_bus_local_subscribers", "Active local event-bus subscriptions on this node.")

	// WorkerIsLeader is 1 while this node holds the background-worker leader lock
	// (and is therefore running the outbox worker + stats scheduler), else 0.
	WorkerIsLeader = NewGauge("budgie_worker_is_leader", "1 if this node is the active background-worker leader, else 0.")
	// SSH sessions and outbox counts are exposed via RegisterCollector at
	// startup (see cmd/budgied), since they are computed on demand.

	// Event-bus counters.
	EventsPublishedLocal         = NewCounter("budgie_events_published_local_total", "Events delivered to a local subscriber channel.")
	EventsPublishedRemote        = NewCounter("budgie_events_published_remote_total", "Events forwarded to the cluster broker for sibling nodes.")
	EventsIngestedRemote         = NewCounter("budgie_events_ingested_remote_total", "Events received from a sibling node and republished locally.")
	RemotePublishFailures        = NewCounter("budgie_events_remote_publish_failures_total", "Events that failed to publish to the cluster broker.")
	RemoteDecodeFailures         = NewCounter("budgie_events_remote_decode_failures_total", "Cluster broker messages that could not be decoded into local events.")
	DroppedSubscriberSends       = NewCounter("budgie_bus_dropped_sends_total", "Live events dropped because a local subscriber channel was full.")
	GatewayDroppedSendsByScope   = NewCounterVec("budgie_gateway_dropped_sends_total", "Live events dropped because a gateway connection queue was full, split by event scope.", []string{"scope"})
	GatewayReconnects            = NewCounter("budgie_gateway_reconnects_total", "Gateway stream resumes with a non-zero durable cursor.")
	GatewayReplayRepairs         = NewCounter("budgie_gateway_replay_repairs_total", "Live delivery gaps repaired by replaying from the durable event log.")
	WriteRegionRoutedRequests    = NewCounterVec("budgie_write_region_routed_requests_total", "Mutating HTTP API requests routed to the authoritative write region, split by method.", []string{"method"})
	WriteRegionProxyFailures     = NewCounter("budgie_write_region_proxy_failures_total", "Mutating HTTP API requests that could not be proxied to the authoritative write region.")
	EventLogShadowAppendFailures = NewCounter("budgie_event_log_shadow_append_failures_total",
		"Shadow event-log appends that failed while the primary event log remained authoritative.")
	EventLogShadowParityFailures = NewCounter("budgie_event_log_shadow_parity_failures_total",
		"Shadow event-log replay, coverage, or append comparisons that did not match the primary logical event log.")
	CommandLogAssignmentLosses = NewCounter("budgie_command_log_assignment_losses_total",
		"Command-log writer drains that stopped because partition assignment was lost.")

	// Replay (gap recovery).
	ReplayTotal     = NewCounter("budgie_replay_total", "Durable-event replay operations performed for connections.")
	ReplayBatchSize = NewHistogram("budgie_replay_batch_size", "Number of events returned per replay operation.",
		[]float64{1, 5, 10, 50, 100, 500, 1000})

	// Latencies, in milliseconds.
	CommandLatency = NewHistogram("budgie_command_latency_ms", "Command handler execution latency in milliseconds.",
		[]float64{1, 2, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000})
	WriterLockWait = NewHistogram("budgie_writer_lock_wait_ms", "Time spent waiting to acquire the writer advisory lock, in milliseconds.",
		[]float64{1, 2, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000})
	ScalarAppendGateWait = NewHistogram("budgie_scalar_append_gate_wait_ms", "Time spent waiting for the scalar seq append gate, in milliseconds.",
		[]float64{1, 2, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000})
	RemoteWakeupLag = NewHistogram("budgie_remote_wakeup_lag_ms", "Delay between a sibling event's timestamp and its local receipt, in milliseconds.",
		[]float64{5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000})
	GatewayWSSendLatency = NewHistogram("budgie_gateway_ws_send_latency_ms", "WebSocket gateway JSON write latency in milliseconds.",
		[]float64{0.1, 0.5, 1, 2, 5, 10, 25, 50, 100, 250, 500, 1000})
	GatewaySSESendLatency = NewHistogram("budgie_gateway_sse_send_latency_ms", "SSE gateway event write latency in milliseconds.",
		[]float64{0.1, 0.5, 1, 2, 5, 10, 25, 50, 100, 250, 500, 1000})
)
