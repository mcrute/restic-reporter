package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"code.crute.us/mcrute/golib/secrets"
	"github.com/go-co-op/gocron/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func main() {
	var err error

	// Setup Logger
	lcfg := zap.NewProductionConfig()
	lcfg.Level.SetLevel(zapcore.DebugLevel)
	logger, _ := lcfg.Build()

	// Process command-line args
	bind := flag.String("bind", ":9121", "Bind address for http server")
	configFile := flag.String("config", "config.json", "Path to configuration file")
	timeZone := flag.String("timezone", "America/Los_Angeles", "Time zone to use for scheduling")
	instanceName := flag.String("instance", "", "Instance name to use in reporting metrics")
	cronExpression := flag.String("cron", "0 0 * * *", "Cron expression for how often to gater repo metrics")
	flag.Parse()

	if *instanceName == "" {
		logger.Fatal("--instance is a required argument")
	}

	// Setup application context
	ctx, cancelMain := context.WithCancel(context.Background())
	defer cancelMain()

	sigs := make(chan os.Signal, 10)
	signal.Notify(sigs, syscall.SIGHUP, syscall.SIGUSR1, syscall.SIGINT)

	var sc secrets.ClientManager
	if os.Getenv("VAULT_ADDR") == "" {
		logger.Warn("VAULT_ADDR not found in environment, Vault is disabled")
	} else {
		if sc, err = secrets.NewVaultClient(nil); err != nil {
			logger.Fatal("Error configuring vault", zap.Error(err))
		}

		if err := sc.Authenticate(ctx); err != nil {
			logger.Fatal("Error authenticating vault", zap.Error(err))
		}
	}

	collector := NewResticCollector(*instanceName, logger)
	prometheus.MustRegister(collector)

	if err := collector.ReloadConfig(ctx, *configFile, sc); err != nil {
		logger.Fatal("Error loading configuration", zap.Error(err))
	}

	tz, err := time.LoadLocation(*timeZone)
	if err != nil {
		logger.Fatal("Error loading timezone", zap.Error(err))
	}

	sched, err := gocron.NewScheduler(gocron.WithLocation(tz))
	if err != nil {
		logger.Fatal("Error configuring scheduler", zap.Error(err))
	}

	_, err = sched.NewJob(
		gocron.CronJob(*cronExpression, true),
		gocron.NewTask(collector.GatherMetrics, ctx),
	)
	if err != nil {
		logger.Fatal("Error adding job to scheduler", zap.Error(err))
	}

	sched.Start()

	logger.Info("Synchronously collecting metrics once at startup")
	collector.GatherMetrics(ctx)

	// Setup and run the HTTP server
	httpMux := http.NewServeMux()
	httpServer := &http.Server{Addr: *bind, Handler: httpMux}
	httpMux.Handle("/metrics", promhttp.Handler())

	httpMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<h1>Restic Exporter</h1><pre><a href="/metrics">/metrics</a></pre>`)
	})

	go func() {
		logger.Info("HTTP server listening", zap.String("port", *bind))
		if err := httpServer.ListenAndServe(); err != nil {
			logger.Error("Error running web server", zap.Error(err))
		}
	}()

	for {
		select {
		case sig := <-sigs:
			switch sig {
			case syscall.SIGHUP:
				logger.Info("SIGHUP received, reloading configuration")
				if err := collector.ReloadConfig(ctx, *configFile, sc); err != nil {
					logger.Error("Error reloading configuration", zap.Error(err))
				}
			case syscall.SIGUSR1:
				logger.Info("SIGUSR1 received, starting repo stats collection")
				go collector.GatherMetrics(ctx)
			case syscall.SIGINT:
				logger.Info("SIGINT received, starting repo stats collection")
				cancelMain()
			}
		case <-ctx.Done():
			logger.Info("Shutdown requested, gracefulling cleaning up for 1 minute")

			shutdownCtx, shutdownCtxCancel := context.WithTimeout(context.Background(), time.Minute)
			defer shutdownCtxCancel()

			httpServer.Shutdown(shutdownCtx)
			collector.Shutdown()
			sched.Shutdown()

			return
		}
	}
}
