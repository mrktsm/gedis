package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mrktsm/gedis/internal/aof"
	"github.com/mrktsm/gedis/internal/replication"
	"github.com/mrktsm/gedis/internal/resp"
	"github.com/mrktsm/gedis/internal/server"
	"github.com/mrktsm/gedis/internal/store"
)

const shutdownTimeout = 5 * time.Second

type options struct {
	address             string
	readTimeout         time.Duration
	writeTimeout        time.Duration
	maxBulkLength       int
	maxArrayLength      int
	expireInterval      time.Duration
	appendOnly          bool
	aofPath             string
	aofSyncPolicy       aof.SyncPolicy
	repairAOF           bool
	replBacklogBytes    int
	replSubscriberQueue int
}

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

func run(arguments []string, output io.Writer) int {
	options, err := parseOptions(arguments, output)
	if err != nil {
		return 2
	}

	logger := slog.New(slog.NewTextHandler(output, nil))
	return runServer(options, logger)
}

func runServer(options options, logger *slog.Logger) (exitCode int) {
	keyspace := store.New()
	var durableSink replication.MutationSink
	if options.appendOnly {
		replay, repaired, err := recoverAOF(options.aofPath, keyspace, options.repairAOF)
		if err != nil {
			logger.Error("failed to load append-only file", "path", options.aofPath, "error", err)
			return 1
		}
		logger.Info(
			"append-only file loaded",
			"path", options.aofPath,
			"commands", replay.Commands,
			"valid_bytes", replay.ValidBytes,
			"repaired", repaired,
		)

		appendLog, err := aof.Open(aof.Config{
			Path:       options.aofPath,
			SyncPolicy: options.aofSyncPolicy,
		})
		if err != nil {
			logger.Error("failed to open append-only file", "path", options.aofPath, "error", err)
			return 1
		}
		defer func() {
			if err := appendLog.Close(); err != nil {
				logger.Error("failed to close append-only file", "path", options.aofPath, "error", err)
				exitCode = 1
			}
		}()
		durableSink = appendLog
	}

	primary, err := replication.NewPrimary(replication.PrimaryConfig{
		BacklogBytes:    options.replBacklogBytes,
		SubscriberQueue: options.replSubscriberQueue,
		Downstream:      durableSink,
	})
	if err != nil {
		logger.Error("failed to initialize replication", "error", err)
		return 1
	}
	engine := server.NewEngineWithStoreAndSink(keyspace, primary)
	primary.SetSnapshotter(engine)
	defer engine.WaitForAOFRewrite()

	listener, err := net.Listen("tcp", options.address)
	if err != nil {
		logger.Error("failed to listen", "address", options.address, "error", err)
		return 1
	}

	config := server.Config{
		ReadTimeout:  options.readTimeout,
		WriteTimeout: options.writeTimeout,
		ProtocolLimits: resp.Limits{
			MaxBulkLength:  options.maxBulkLength,
			MaxArrayLength: options.maxArrayLength,
		},
		ConnectionCommandHandler: primary,
	}
	gedis := server.New(config, engine)
	expirationContext, stopExpiration := context.WithCancel(context.Background())
	expirationDone := make(chan struct{})
	go func() {
		keyspace.RunExpiration(expirationContext, options.expireInterval, 1000)
		close(expirationDone)
	}()
	defer func() {
		stopExpiration()
		<-expirationDone
	}()

	signalContext, stopSignals := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stopSignals()

	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- gedis.Serve(listener)
	}()

	logger.Info("Gedis is accepting RESP2 connections", "address", listener.Addr().String())
	select {
	case err := <-serveErrors:
		if err != nil {
			logger.Error("server stopped", "error", err)
			return 1
		}
		return 0
	case <-signalContext.Done():
		logger.Info("shutting down")
	}

	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancelShutdown()
	if err := gedis.Shutdown(shutdownContext); err != nil {
		logger.Error("shutdown failed", "error", err)
		return 1
	}
	if err := <-serveErrors; err != nil {
		logger.Error("server stopped", "error", err)
		return 1
	}
	return 0
}

func parseOptions(arguments []string, output io.Writer) (options, error) {
	defaults := resp.DefaultLimits
	parsed := options{}
	aofSyncPolicy := string(aof.SyncEverySecond)

	flags := flag.NewFlagSet("gedis", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&parsed.address, "addr", "127.0.0.1:6379", "TCP address to listen on")
	flags.DurationVar(&parsed.readTimeout, "read-timeout", 0, "per-command read timeout; zero disables it")
	flags.DurationVar(&parsed.writeTimeout, "write-timeout", 5*time.Second, "response write timeout")
	flags.IntVar(&parsed.maxBulkLength, "max-bulk-bytes", defaults.MaxBulkLength, "maximum RESP bulk string size")
	flags.IntVar(&parsed.maxArrayLength, "max-array-length", defaults.MaxArrayLength, "maximum RESP array length")
	flags.DurationVar(&parsed.expireInterval, "expire-interval", 100*time.Millisecond, "active expiration interval")
	flags.BoolVar(&parsed.appendOnly, "appendonly", false, "enable append-only persistence")
	flags.StringVar(&parsed.aofPath, "aof-path", "data/appendonly.aof", "append-only file path")
	flags.StringVar(&aofSyncPolicy, "appendfsync", aofSyncPolicy, "AOF fsync policy: always, everysec, or no")
	flags.BoolVar(&parsed.repairAOF, "aof-repair-truncated", false, "truncate an incomplete final AOF command during startup")
	flags.IntVar(&parsed.replBacklogBytes, "repl-backlog-bytes", 1024*1024, "maximum retained replication stream bytes")
	flags.IntVar(&parsed.replSubscriberQueue, "repl-subscriber-queue", 256, "maximum queued mutations per replica")
	if err := flags.Parse(arguments); err != nil {
		return options{}, err
	}
	parsed.aofSyncPolicy = aof.SyncPolicy(aofSyncPolicy)
	if flags.NArg() != 0 {
		return options{}, fmt.Errorf("unexpected positional arguments: %v", flags.Args())
	}
	if parsed.address == "" {
		return options{}, errors.New("addr cannot be empty")
	}
	if parsed.readTimeout < 0 || parsed.writeTimeout < 0 {
		return options{}, errors.New("timeouts cannot be negative")
	}
	if parsed.expireInterval <= 0 {
		return options{}, errors.New("expire-interval must be positive")
	}
	if parsed.maxBulkLength <= 0 || parsed.maxArrayLength <= 0 {
		return options{}, errors.New("protocol limits must be positive")
	}
	if parsed.aofSyncPolicy != aof.SyncAlways &&
		parsed.aofSyncPolicy != aof.SyncEverySecond &&
		parsed.aofSyncPolicy != aof.SyncNever {
		return options{}, errors.New("appendfsync must be always, everysec, or no")
	}
	if parsed.appendOnly && parsed.aofPath == "" {
		return options{}, errors.New("aof-path cannot be empty when appendonly is enabled")
	}
	if parsed.replBacklogBytes <= 0 {
		return options{}, errors.New("repl-backlog-bytes must be positive")
	}
	if parsed.replSubscriberQueue <= 0 {
		return options{}, errors.New("repl-subscriber-queue must be positive")
	}
	return parsed, nil
}
