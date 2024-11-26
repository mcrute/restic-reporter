package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
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
	cronExpression := flag.String("cron", "0 0 * * *", "Cron expression for how often to gather repo metrics")
	flag.Parse()

	// Setup application context
	ctx, cancelMain := context.WithCancel(context.Background())
	defer cancelMain()

	// Handle various signals
	sigs := make(chan os.Signal, 10)
	signal.Notify(sigs, syscall.SIGHUP, syscall.SIGUSR1, syscall.SIGINT)

	// Setup secret client if possible
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

		go sc.Run(ctx, &sync.WaitGroup{})
	}

	// Setup the collector and load config
	collector := NewResticCollector(logger)
	prometheus.MustRegister(collector)

	if err := collector.ReloadConfig(ctx, *configFile, sc); err != nil {
		logger.Fatal("Error loading configuration", zap.Error(err))
	}

	// Uses time.Local as time zone, which considers the TZ environment
	// variable override. Export that if needed.
	sched, err := gocron.NewScheduler()
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
