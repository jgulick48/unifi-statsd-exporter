package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/jgulick48/unifi-statsd-exporter/internal/config"
	"github.com/jgulick48/unifi-statsd-exporter/internal/metrics"
	"github.com/jgulick48/unifi-statsd-exporter/internal/snmp"
	"github.com/jgulick48/unifi-statsd-exporter/internal/unifi"
)

// Set at build time via -ldflags.
var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	configFile := flag.String("configFile", "", "path to the YAML config file")
	flag.Parse()

	log.Printf("unifi-statsd-exporter %s (%s)", version, commit)

	cfg, err := config.Load(*configFile)
	if err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}

	client, err := unifi.NewClient(cfg.UniFi)
	if err != nil {
		log.Fatalf("creating UniFi client: %v", err)
	}

	reporter, statsdClient, err := metrics.New(cfg.Statsd, cfg.UniFi.Site)
	if err != nil {
		log.Fatalf("creating statsd client: %v", err)
	}
	defer statsdClient.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup
	if cfg.SNMPTraps.Enabled {
		listener := snmp.NewListener(cfg.SNMPTraps, reporter.ReportTrap)
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := listener.Run(ctx); err != nil {
				log.Printf("SNMP trap listener stopped: %v", err)
				stop()
			}
		}()
	}

	log.Printf("polling %s (site %q) every %s", cfg.UniFi.URL, cfg.UniFi.Site, cfg.UniFi.PollInterval)
	poll(ctx, client, reporter)
	ticker := time.NewTicker(cfg.UniFi.PollInterval)
	defer ticker.Stop()
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case <-ticker.C:
			poll(ctx, client, reporter)
		}
	}

	log.Print("shutting down")
	wg.Wait()
}

func poll(ctx context.Context, client *unifi.Client, reporter *metrics.Reporter) {
	start := time.Now()
	devices, err := client.Devices(ctx)
	reporter.ReportPoll(time.Since(start), err)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("polling devices: %v", err)
		}
		return
	}
	if err := client.LastClientsError(); err != nil {
		log.Printf("polling clients (ports reported without connection info): %v", err)
	}
	reporter.ReportDevices(devices)
}
