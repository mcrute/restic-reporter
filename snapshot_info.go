package main

import (
	"fmt"
	"time"
)

type snapshotInfo struct {
	Host     string
	Username string
	Time     time.Time
	Count    int
}

// DayAge computes the days age of the snapshot from some time now. now
// is passed in to allow evaluating all snapshots from a static point in
// time.
func (i snapshotInfo) DayAge(now time.Time) int {
	return int(now.Sub(i.Time).Hours() / 24)
}

// IsLegacy indicates if a snapshot should be considered legacy. This
// method is itself legacy and is preserved only to prevent invalidating
// years of original metrics.
//
// The original implementation of what is now this daemon added a label
// of isLegacy to metrics once they stopped being updated for over
// 60 days. This indicated to the alerting rules in prometheus that
// a backup set had "aged-out" and was a way to allow upgrading or
// deprecating devices without forever having them in alarm. It turns
// out there's a much easier way to do that with compound comparisons
// but since prometheus considers metrics to be unique by their labels
// then dropping this would be problematic and invalidate years of
// metrics. So this is preserved. It should not have existed but
// sometimes it's hard to abandon the errors of the past.
func (i snapshotInfo) IsLegacy() bool {
	return i.DayAge(time.Now()) > 60
}

// SnapshotCollection holds a collection of snapshots indexed by the
// hostname and username that took them.
type SnapshotCollection map[string]*snapshotInfo

// Add adds a snapshot from a restic repository using some summary data.
//
// They key for the snapshot is the hostname and username that produced
// the snapshot. This assumes that multiple hosts and users may be
// packed into a single repository (but should work well even if that's
// not true).
//
// snapshotInfo.Time will always be the latest time of any snapshot
// found. Count will be the total number of snapshots found for the
// collection.
//
// This uses some summary info from the snapshot rather than the whole
// snapshot to eliminate hard dependencies on the internals of restic.
func (c SnapshotCollection) Add(username, hostname string, snapshotTime time.Time) {
	// An older version of restic had a bug where on macOS in some cases
	// it would set an empty username. This bug no longer exists but this
	// patches over old snapshots that still have invalid data.
	if username == "" {
		username = "UNKNOWN"
	}

	key := fmt.Sprintf("%s-%s", hostname, username)

	val := c[key]
	if val == nil {
		val = &snapshotInfo{
			Host:     hostname,
			Username: username,
		}
		c[key] = val
	}

	if val.Time.Before(snapshotTime) {
		val.Time = snapshotTime
	}

	val.Count += 1
}
