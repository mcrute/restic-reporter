package main

import "github.com/prometheus/client_golang/prometheus"

const (
	namespace = "backup"
)

var (
	lastSuccessTime = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "job_last_success_unixtime"),
		"Last time a batch job successfully finished",
		nil, nil,
	)
	jobErrorCount = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "job_error_count"),
		"Number of errors encountered by backup monitoring job",
		nil, nil,
	)
	readErrorCount = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "read_error_count"),
		"Number of errors encountered when reading backup",
		[]string{"url"}, nil,
	)
	// See note on SnapshotCollection.IsLegacy for more info about isLegacy
	snapshotCount = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "snapshot_count"),
		"Number of snapshots in a backup set",
		[]string{"url", "host", "user", "isLegacy"}, nil,
	)
	newestTimestamp = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "newest_timestamp"),
		"Most recent snapshot timestamp in backup set",
		[]string{"url", "host", "user", "isLegacy"}, nil,
	)
	backupSetDayAge = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "days_age"),
		"Age in days since the most recent backup in a backup set",
		[]string{"url", "host", "user"}, nil,
	)
)
