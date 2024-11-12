# Restic Backup Prometheus Exporter

This is a Prometheus exporter for a set of
[Restic](https://restic.net) backups that can either be stored behind
a [restic-rest-server](https://github.com/restic/rest-server) instance
or in [Backblaze B2](https://www.backblaze.com/cloud-storage). Although
this could be extended pretty easily to support any of the backends that
restic supports.

The goal of this project is to allow organizations that have a large
set of distributed backup processes to monitor that those backups are
succeeding on a regular basis and to configure alarms should they start
to fail.

Upon startup and every time a cron expression occurs thereafter,
the collector will enumerate all snapshots in one or more restic
repositories and export the date of the most recent snapshot and the
age in days of the most recent snapshot as prometheus metrics (see
Metrics below for the full details). This information can be scraped by
Prometheus and used to configure alert rules that trigger when a backup
set is too old, indicating that backups may be failing.

This is a rewrite of some rather old (and somewhat convoluted) python
scripts that used to collect and push metrics via cron files to
pushgateway so there are some legacy patterns in the code that may seem
odd based on that heritage.

## Metrics

All metrics exposed are gauges, even where it may seem to make sense for
them to be counters. The reason for this is that backup sets are mutable
over time and counters may roll backwards (for example, when pruning old
backups).

The metrics are reported for "backup sets" a concept for which there
is no analog in restic. A backup set is defined by this exporter as
the combination of a repository url, user, and hostname. This allows
multiple hosts and multiple users on those hosts to backup into the same
restic repository and still be considered different backups for the
purpose of reporting metrics. The paths of the backup are not considered
to be a grouping critera for backup sets.

The following gauge metrics are exposed:

* `backup_job_last_success_unixtime` - the last time that the entire
  job successfully ran. Note that this doesn't mean that every repository
  was successfully collected, just that the job as a whole finished with
  success.
* `backup_job_error_count` - the number of errors that occurred while
   reading repositories in the latest run of the job.

The following metrics use the `url` label to indicate the repository for
which the metric reports.

* `backup_read_error_count` - the number of errors that occurred while
  collecting metrics for an individual repository. Should always be 0
  for success and 1 for failure.
* `backup_snapshot_count` - the number of snapshots in a repository.
* `backup_newest_timestamp` - the Unix timestamp of the most recent
  snapshot in the repository. Contains `host` and `user` labels to
  differentiate multiple backups within a single repository.
* `backup_days_age` - the number days from today that the most recent
   snapshot was taken. This is a convenience to avoid needing to do date
   math on `backup_newest_timestamp` in Prometheus. Uses the same labels
   as that metric.

## Building

The restic codebase is weird and poorly factored with almost the entire
API hidden behind an `internal` package and the rest in an un-importable
`main` package. So building this has to be done within the restic source
tree. The easiest way to do this is as follows:

```
git clone https://github.com/restic/restic.git
cd restic
git clone https://github.com/mcrute/restic-reporter.git reporter
cd reporter
make
```

This will update the restic go.mod and then build the exporter.

## Configuring

The exporter requires a configuration file for the repositories which
are to be collected. The file is called `config.json` by default, which
can be overridden with the `--config` flag on the command line. The file
is a JSON list containing hash maps for each repository. The structure
of those maps is:

* `disabled` (boolean) - indicates that the repository should not be
   collected. Default: false
* `repo` (string) - the URL for the repository in restic style (e.g.
   `rest:http://...`)
* `password` (string) - the password to decrypt the restic repository.
  This is optional and if not specified then `vault_material` must be
  specific and that will be used to load the password.
* `vault_material` (string) - a path to a key/value material in
  Hashicorp Vault that contains a JSON document with a `key` that contains
  the password for the repository. At config load time this will be looked
  up to fill the `password` in the config. This assumes that the Key/Value
  store is mounted to the path `kv/`. This is optional but if it is not
  specified then `password` must be specified.
* `b2_vault_material` (string) - similar to `vault_material` but
  containing a `id` and `key` with credentials for a Backblaze B2 account.
  This is optional and only applicable if the repository is in B2.
* `b2_account_id` (string) - B2 account ID for connecting to Backblaze
  B2. This is optional and only used if the repository is stored in
  Backblaze B2. This is an alternative to `b2_vault_material`.
* `b2_key` (string) - B2 secret key for connecting to Backblaze B2. This
  is optional and only used if the repository is stored in Backblaze B2.
  This is an alternative to `b2_vault_material`.

Example:

```json
[
    {
        "repo": "rest:https://backups.example.com/my-repo",
        "vault_material": "service/backups/my-repo-key"
    },
    {
        "repo": "rest:https://backups.example.com/my-repo-too",
        "password": "foo",
    },
    {
        "repo": "b2:my-backup-bucket:",
        "vault_material": "service/backups/my-b2-backups-key",
        "b2_vault_material": "service/backups/b2-account-keys"
    },
    {
        "disabled": true,
        "repo": "b2:my-backup-bucket-too:",
        "vault_material": "service/backups/my-b2-backups-key-too",
        "b2_account_id": "12345",
        "b2_key": "my-secret-key"
    }
]
```

## Running

The exporter can be run by running the executable. Once started a
synchronous collection of all enabled repositories in the configuration
file will be completed and then the web server will start. After that
point collections will occur based on a cron expression. This prevents
starting the server and having it scraped with no metrics available.
After the initial collection metrics will always be available for all
repositories.

### Environment

If using the Hashicorp Vault integration for storing secrets
(recommended) then the following environment variables must be exported
before starting the process.

* `VAULT_ADDR` the HTTP/S address the Vault server. The presence of this
  variable in the environment indicates to the application to start Vault
  support. If this is missing Vault will be skipped and the application
  will start normally.
* `VAULT_TOKEN` (optional) a Vault token to use for authentication
* `VAULT_ROLE_ID` and `VAULT_SECRET_ID` (optional) used to authenticate
  to Vault using the AppRole backend. Either these or `VAULT_TOKEN` must
  be specified otherwise Vault will fail to initialize.

### Command-Line Flags

The following command line flags are supported:

* `--help` - shows help
* `--bind` (default: `:9121`) - bind address for the HTTP server
* `--config` (default: `config.json`) - the path to the configuration file
* `cron` (default: `0 0 * * *`) - the cron expression used for scheduling
  when repository scrapes should occur. By default this is midnight in the
  local timezone every day.

### Signals

The exporter supports a few signals to allow runtime reconfiguration.

* `HUP` - causes the server to reload the configuration file. The
  configuration swap is atomic internally so it is safe to do this while a
  collection is running but note that the configuration changes will not
  take effect until the next scheduled collection.
* `USR1` - causes the server to immediately start a collection for all
  repositories. If a collection is already running a log message will be
  printed and this signal is a no-op.
* `INT` - causes the server to cleanly shut down, releasing all
  repository locks and satisfying any in-flight scrapes.

### Scraping

Metrics are exposed over HTTP at the `/metrics` endpoint as is standard
for Prometheus exporters. Example scrape config:

```
scrape_configs:
  - job_name: 'restic-backups'
    static_configs:
    - targets: 
      - 'restic-backup-reporter-host:9121'
```

### Monitoring Examples

The following is an example of a set of Prometheus alert rules that
monitor the job and backup set age.

```
groups:
- name: backups
  rules:
  - alert: Backup job failures 
    expr: backup_job_error_count > 0
  - alert: Backup read failures 
    expr: backup_read_error_count > 0
  - alert: Backup metrics age too old
    expr: round((time() - job_last_success_unixtime{job="backupReporter"}) / 86400) >= 2
  - alert: Backup set age too old
    expr: backup_days_age > 3
```

## Contributing

Contributions are welcomed. Please file a pull request and we'll
consider your changes. Please try to follow the style of the existing
code and do not add additional libraries without justification.

While we appreciate the time and effort of contributors there's not
guarantee that we'll be able to accept all contributions. If you're
interested in making a rather large change then please open an issue
first so we can discuss the implications of the change before you invest
too much time in making those changes.
