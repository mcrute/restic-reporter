package main

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"code.crute.us/mcrute/golib/secrets"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
)

type allRepoMetrics struct {
	Time   time.Time
	Errors int
	Stats  []repoStats
}

type repoStats struct {
	Name       string
	ReadErrors int
	Stats      SnapshotCollection
}

type ResticCollector struct {
	InstanceName string
	config       atomic.Pointer[ConfigFile]
	metrics      atomic.Pointer[allRepoMetrics]
	wait         *sync.WaitGroup // held by gatherOne to prevent leaving stale locks
	logger       *zap.Logger
	sync.Mutex   // prevents concurrent collections
}

func NewResticCollector(instanceName string, logger *zap.Logger) *ResticCollector {
	return &ResticCollector{
		InstanceName: instanceName,
		wait:         &sync.WaitGroup{},
		logger:       logger,
	}
}

func (c *ResticCollector) ReloadConfig(ctx context.Context, filename string, sc secrets.Client) error {
	cfg, err := NewConfigFileFromFile(ctx, filename, sc)
	if err != nil {
		return err
	}
	c.config.Store(&cfg)
	return nil
}

func (c *ResticCollector) gatherOne(ctx context.Context, cfg *configEntry, done chan repoStats) {
	c.wait.Add(1)
	defer c.wait.Done()

	repo, lock, ctx, err := openResticBackend(ctx, cfg.Repo, cfg.Password, cfg.ExtraConfig())
	if err != nil {
		c.logger.Error("Error opening restic backend", zap.String("repo", cfg.Repo), zap.Error(err))
		done <- repoStats{Name: cfg.Repo, ReadErrors: 1}
		return
	}
	defer lock.Unlock()

	col, err := collectionFromAllSnapshots(ctx, repo)
	if err != nil {
		c.logger.Error("Error iterating restic snapshots", zap.String("repo", cfg.Repo), zap.Error(err))
		done <- repoStats{Name: cfg.Repo, ReadErrors: 1}
		return
	}

	done <- repoStats{Name: cfg.Repo, Stats: col}
}

func (c *ResticCollector) GatherMetrics(ctx context.Context) {
	if !c.TryLock() {
		c.logger.Error("GatherMetrics already running, can not start another instance")
		return
	}
	defer c.Unlock()

	cfg := *c.config.Load()

	started := 0
	done := make(chan repoStats, len(cfg))

	for _, entry := range cfg {
		if !entry.Disabled {
			c.logger.Debug("Collecting repo", zap.String("repo", entry.Repo))
			started += 1
			go c.gatherOne(ctx, entry, done)
		}
	}

	metrics := allRepoMetrics{
		Stats: make([]repoStats, 0, len(cfg)),
	}

	for {
		select {
		case stats := <-done:
			c.logger.Debug("Finished collecting repo", zap.String("repo", stats.Name))

			metrics.Stats = append(metrics.Stats, stats)

			if stats.ReadErrors > 0 {
				metrics.Errors += 1
			}

			if len(metrics.Stats) == started {
				metrics.Time = time.Now()
				c.metrics.Store(&metrics)
				c.logger.Debug("All jobs done")
				return
			}
		}
	}
}

func (c *ResticCollector) Shutdown() {
	c.wait.Wait()
}

func (c *ResticCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- lastSuccessTime
	ch <- jobErrorCount
	ch <- readErrorCount
	ch <- snapshotCount
	ch <- newestTimestamp
	ch <- backupSetDayAge
}

func (c *ResticCollector) Collect(ch chan<- prometheus.Metric) {
	now := time.Now()
	metrics := c.metrics.Load()

	ch <- prometheus.MustNewConstMetric(
		lastSuccessTime, prometheus.GaugeValue, float64(metrics.Time.UnixNano())/1e9,
		c.InstanceName, jobName,
	)

	ch <- prometheus.MustNewConstMetric(
		jobErrorCount, prometheus.GaugeValue, float64(metrics.Errors),
		c.InstanceName, jobName,
	)

	for _, stats := range metrics.Stats {
		ch <- prometheus.MustNewConstMetric(
			readErrorCount, prometheus.GaugeValue, float64(stats.ReadErrors),
			c.InstanceName, stats.Name, jobName,
		)

		for _, set := range stats.Stats {
			ch <- prometheus.MustNewConstMetric(
				snapshotCount, prometheus.GaugeValue, float64(set.Count),
				c.InstanceName, stats.Name, set.Host, set.Username, jobName,
			)
			ch <- prometheus.MustNewConstMetric(
				newestTimestamp, prometheus.GaugeValue, float64(set.Time.Unix()),
				c.InstanceName, stats.Name, set.Host, set.Username, jobName,
			)
			ch <- prometheus.MustNewConstMetric(
				backupSetDayAge, prometheus.GaugeValue, float64(set.DayAge(now)),
				c.InstanceName, stats.Name, set.Host, set.Username, jobName,
			)
		}
	}
}
