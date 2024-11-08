package main

// This whole file exists to abstract away a bunch of the complexities
// of restic and to access the internal APIs. Everything about the
// exporter that touches restic internals is, intentionally, in this
// file.
//
// Restic is a very poorly factored codebase where a large chunk of
// the logic lives in the command line implementation and the rest
// of it lives in an internal package that can't be imported outside
// of the source tree. The functions here are largely a simplified
// re-implementation of the snapshot command line designed purely for
// the exporter. They're much less feature rich and support fewer
// backends but are otherwise functional for a Prometheus exporter.

import (
	"context"
	"fmt"
	"time"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/backend/b2"
	"github.com/restic/restic/internal/backend/limiter"
	"github.com/restic/restic/internal/backend/location"
	"github.com/restic/restic/internal/backend/logger"
	"github.com/restic/restic/internal/backend/rest"
	"github.com/restic/restic/internal/backend/retry"
	"github.com/restic/restic/internal/backend/sema"
	"github.com/restic/restic/internal/options"
	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
)

// openResticBackend opens a restic repository and takes a read lock on
// it. The caller is responsible for unlocking the lock when they no
// longer need it. The lock returns a context which should be used as a
// replacement for the context passed into this function.
//
// This is largely a less options-driven version of the logic in
// cmd/restic/global:OpenRepository which can't easily be used because
// it's both command line flag driven and in a non-importable `main`
// package.
//
// Supporting more than B2 and REST will require updates to this function.
func openResticBackend(ctx context.Context, uri, cryptoKey string, extraConfig any) (*repository.Repository, *repository.Unlocker, context.Context, error) {
	// Populate a location registry with only the supported backends.
	// More could be easily supported but because each backend may need
	// some additional configuration that's type specific they aren't all
	// supported by default.
	backends := location.NewRegistry()
	backends.Register(b2.NewFactory())
	backends.Register(rest.NewFactory())

	loc, err := location.Parse(backends, uri)
	if err != nil {
		return nil, nil, nil, err
	}

	// Basically an http.DefaultTransport with some shorthand
	// configuration. Sticking with this version since it'll deviate from
	// API expectations less. Although http.DefaultTransport should really
	// be just fine.
	rt, err := backend.Transport(backend.TransportOptions{})
	if err != nil {
		return nil, nil, nil, err
	}

	// Only really needed because factory.Open expects it. It should in
	// theory be safe to pass nil there but it's less fragile to just setup
	// an unlimited Limiter instead.
	lim := limiter.NewStaticLimiter(limiter.Limits{
		UploadKb:   0,
		DownloadKb: 0,
	})
	rt = lim.Transport(rt)

	factory := backends.Lookup(loc.Scheme)
	if factory == nil {
		return nil, nil, nil, fmt.Errorf("No such backend type")
	}

	// Applies extra backend specific config. This will possibly need
	// updated to support other backend types.
	switch extraCfg := extraConfig.(type) {
	case b2Config:
		if cfg, ok := loc.Config.(*b2.Config); ok {
			cfg.AccountID = extraCfg.AccountID
			cfg.Key = options.NewSecretString(extraCfg.Key)
		}
	}

	var be backend.Backend
	be, err = factory.Open(ctx, loc.Config, rt, lim)
	if err != nil {
		return nil, nil, nil, err
	}

	be = logger.New(sema.NewBackend(be))

	report := func(msg string, err error, d time.Duration) {
		if d >= 0 {
			fmt.Printf("%v returned error, retrying after %v: %v\n", msg, d, err)
		} else {
			fmt.Printf("%v failed: %v\n", msg, err)
		}
	}
	success := func(msg string, retries int) {
		fmt.Printf("%v operation successful after %d retries\n", msg, retries)
	}
	be = retry.New(be, 15*time.Minute, report, success)

	// Stat the repo config file to make sure we have a valid repository
	// target. Checks to make sure the repo size isn't zero as a double check.
	// This should also fail if the backend is misconfigured.
	fi, err := be.Stat(ctx, backend.Handle{Type: restic.ConfigFile})
	if err != nil {
		return nil, nil, nil, err
	}

	if fi.Size == 0 {
		return nil, nil, nil, fmt.Errorf("Invalid repo size 0")
	}

	// Actually setup the repository, assumes a lot of defaults
	repo, err := repository.New(be, repository.Options{
		Compression: repository.CompressionAuto,
	})
	if err != nil {
		return nil, nil, nil, err
	}

	// Search up to 20 keys before failing to decrypt the repository. 20
	// Scomes from the napshot CLI implementation. The empty string is the
	// SKeyID hint, which shouldn't matter unless we have more than 20 keys
	// Sfor a repository.
	if err := repo.SearchKey(ctx, cryptoKey, 20, ""); err != nil {
		return nil, nil, nil, err
	}

	// Grab a non-exclusive read lock on the repository with no retries
	// to prevent certain admin commands from shuffling data out from
	// underneath of us. This is similar to the logic the snapshot command
	// line uses. The caller must unlock this lock before they're done
	// otherwise the repo will have stale locks and backups may fail.
	var lock *repository.Unlocker
	printRetry := func(msg string) {
		fmt.Printf("Retrying lock: %s\n", msg)
	}
	lockLogger := func(format string, args ...any) {
		fmt.Printf(format, args...)
	}
	lock, ctx, err = repository.Lock(ctx, repo, false /*exclusive*/, 0 /*no retry*/, printRetry, lockLogger)

	return repo, lock, ctx, nil
}

// collectionFromAllSnapshots creates a SnapshotCollection from all
// snapshots in a repository. It really exists to limit the scope of
// what things in the exporter know about the internals of restic.
func collectionFromAllSnapshots(ctx context.Context, repo *repository.Repository) (SnapshotCollection, error) {
	col := SnapshotCollection{}
	err := restic.ForAllSnapshots(ctx, repo, repo, restic.IDSet{}, func(_ restic.ID, sn *restic.Snapshot, err error) error {
		if err != nil {
			return err
		}

		col.Add(sn.Username, sn.Hostname, sn.Time)

		return nil
	})
	return col, err
}
