package metrics

// Process-wide metric handles, named per the W7 observability list in
// milestone-scaling-multiple-servers.md. Counters/gauges are safe to touch from
// any goroutine; histograms record in milliseconds (latency/lag) or raw counts
// (batch size).
var (
	// Connection gauges.
	WSConnections    = NewGauge("budgie_ws_connections", "Open WebSocket connections on this node.")
	LocalSubscribers = NewGauge("budgie_bus_local_subscribers", "Active local event-bus subscriptions on this node.")
	// SSH sessions and outbox counts are exposed via RegisterCollector at
	// startup (see cmd/budgied), since they are computed on demand.

	// Event-bus counters.
	EventsPublishedLocal  = NewCounter("budgie_events_published_local_total", "Events delivered to a local subscriber channel.")
	EventsPublishedRemote = NewCounter("budgie_events_published_remote_total", "Events forwarded to the cluster broker for sibling nodes.")
	EventsIngestedRemote  = NewCounter("budgie_events_ingested_remote_total", "Events received from a sibling node and republished locally.")
	DroppedSubscriberSends = NewCounter("budgie_bus_dropped_sends_total", "Live events dropped because a local subscriber channel was full.")

	// Replay (gap recovery).
	ReplayTotal     = NewCounter("budgie_replay_total", "Durable-event replay operations performed for connections.")
	ReplayBatchSize = NewHistogram("budgie_replay_batch_size", "Number of events returned per replay operation.",
		[]float64{1, 5, 10, 50, 100, 500, 1000})

	// Latencies, in milliseconds.
	CommandLatency = NewHistogram("budgie_command_latency_ms", "Command handler execution latency in milliseconds.",
		[]float64{1, 2, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000})
	WriterLockWait = NewHistogram("budgie_writer_lock_wait_ms", "Time spent waiting to acquire the global write lock, in milliseconds.",
		[]float64{1, 2, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000})
	RemoteWakeupLag = NewHistogram("budgie_remote_wakeup_lag_ms", "Delay between a sibling event's timestamp and its local receipt, in milliseconds.",
		[]float64{5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000})
)
