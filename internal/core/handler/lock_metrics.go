package handler

import (
	"sort"
	"sync"

	"github.com/juncoflockleader/budgie-bbs/internal/metrics"
)

const partitionLockWaitSampleLimit = 100

type partitionLockWaitStat struct {
	partition CommandPartition
	count     int64
	sumMS     float64
	maxMS     float64
}

var (
	partitionLockWaitMu    sync.Mutex
	partitionLockWaitStats = map[string]*partitionLockWaitStat{}
)

func init() {
	metrics.RegisterCollector(func() []metrics.Sample {
		return partitionLockWaitSamples(partitionLockWaitSampleLimit)
	})
}

func observePartitionLockWait(partition CommandPartition, waitMS float64) {
	partition = normalizeCommandPartition(partition)
	key := partition.Kind + "\x00" + partition.Key

	partitionLockWaitMu.Lock()
	stat := partitionLockWaitStats[key]
	if stat == nil {
		stat = &partitionLockWaitStat{partition: partition}
		partitionLockWaitStats[key] = stat
	}
	stat.count++
	stat.sumMS += waitMS
	if waitMS > stat.maxMS {
		stat.maxMS = waitMS
	}
	partitionLockWaitMu.Unlock()
}

func partitionLockWaitSamples(limit int) []metrics.Sample {
	if limit <= 0 {
		limit = partitionLockWaitSampleLimit
	}

	partitionLockWaitMu.Lock()
	stats := make([]partitionLockWaitStat, 0, len(partitionLockWaitStats))
	for _, stat := range partitionLockWaitStats {
		stats = append(stats, *stat)
	}
	partitionLockWaitMu.Unlock()

	sort.Slice(stats, func(i, j int) bool {
		if stats[i].sumMS == stats[j].sumMS {
			if stats[i].partition.Kind == stats[j].partition.Kind {
				return stats[i].partition.Key < stats[j].partition.Key
			}
			return stats[i].partition.Kind < stats[j].partition.Kind
		}
		return stats[i].sumMS > stats[j].sumMS
	})
	if len(stats) > limit {
		stats = stats[:limit]
	}

	samples := make([]metrics.Sample, 0, len(stats)*5)
	for _, stat := range stats {
		labels := map[string]string{
			"kind": stat.partition.Kind,
			"key":  stat.partition.Key,
		}
		samples = append(samples,
			metrics.Sample{
				Name:   "budgie_writer_partition_lock_wait_count",
				Help:   "Command-lock acquisitions observed by write-ordering partition.",
				Type:   "counter",
				Labels: labels,
				Value:  float64(stat.count),
			},
			metrics.Sample{
				Name:   "budgie_writer_partition_lock_wait_ms_sum",
				Help:   "Total command-lock wait time by write-ordering partition, in milliseconds.",
				Type:   "counter",
				Labels: labels,
				Value:  stat.sumMS,
			},
			metrics.Sample{
				Name:   "budgie_writer_partition_lock_wait_ms_max",
				Help:   "Maximum observed command-lock wait time by write-ordering partition, in milliseconds.",
				Type:   "gauge",
				Labels: labels,
				Value:  stat.maxMS,
			},
		)
		if stat.count > 0 {
			samples = append(samples, metrics.Sample{
				Name: "budgie_hot_partition_candidate",
				Help: "Hot write-ordering partition candidate, with value set to the signal magnitude.",
				Type: "gauge",
				Labels: map[string]string{
					"kind":   stat.partition.Kind,
					"key":    stat.partition.Key,
					"signal": "command_count",
				},
				Value: float64(stat.count),
			})
		}
		if stat.maxMS > 0 {
			samples = append(samples, metrics.Sample{
				Name: "budgie_hot_partition_candidate",
				Help: "Hot write-ordering partition candidate, with value set to the signal magnitude.",
				Type: "gauge",
				Labels: map[string]string{
					"kind":   stat.partition.Kind,
					"key":    stat.partition.Key,
					"signal": "writer_lock_wait_ms_max",
				},
				Value: stat.maxMS,
			})
		}
	}
	return samples
}

func resetPartitionLockWaitStatsForTest() {
	partitionLockWaitMu.Lock()
	partitionLockWaitStats = map[string]*partitionLockWaitStat{}
	partitionLockWaitMu.Unlock()
}
