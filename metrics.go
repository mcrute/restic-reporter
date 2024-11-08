package main

import "github.com/prometheus/client_golang/prometheus"

// job and jobName are relics of the original version of this being a
// cron job that pushed to pushgateway

const (
	namespace = "backup"
	jobName   = "backupReporter"
)

var (
	lastSuccessTime = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "job_last_success_unixtime"),
		"Last time a batch job successfully finished",
		[]string{"instance", "job"}, nil,
	)
	jobErrorCount = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "job_error_count"),
		"Number of errors encountered by backup monitoring job",
		[]string{"instance", "job"}, nil,
	)
	readErrorCount = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "read_error_count"),
		"Number of errors encountered when reading backup",
		[]string{"instance", "url", "job"}, nil,
	)
	snapshotCount = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "snapshot_count"),
		"Number of snapshots in a backup set",
		[]string{"instance", "url", "host", "user", "job"}, nil,
	)
	newestTimestamp = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "newest_timestamp"),
		"Most recent snapshot timestamp in backup set",
		[]string{"instance", "url", "host", "user", "job"}, nil,
	)
	backupSetDayAge = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "days_age"),
		"Age in days since the most recent backup in a backup set",
		[]string{"instance", "url", "host", "user", "job"}, nil,
	)
)
