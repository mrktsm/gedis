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

	"github.com/mrktsm/gedis/internal/resp"
	"github.com/mrktsm/gedis/internal/server"
	"github.com/mrktsm/gedis/internal/store"
)

const shutdownTimeout = 5 * time.Second

type options struct {
	address        string
	readTimeout    time.Duration
	writeTimeout   time.Duration
	maxBulkLength  int
	maxArrayLength int
	expireInterval time.Duration
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
	}
	keyspace := store.New()
	gedis := server.New(config, server.NewEngineWithStore(keyspace))
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

	flags := flag.NewFlagSet("gedis", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&parsed.address, "addr", "127.0.0.1:6379", "TCP address to listen on")
	flags.DurationVar(&parsed.readTimeout, "read-timeout", 0, "per-command read timeout; zero disables it")
	flags.DurationVar(&parsed.writeTimeout, "write-timeout", 5*time.Second, "response write timeout")
	flags.IntVar(&parsed.maxBulkLength, "max-bulk-bytes", defaults.MaxBulkLength, "maximum RESP bulk string size")
	flags.IntVar(&parsed.maxArrayLength, "max-array-length", defaults.MaxArrayLength, "maximum RESP array length")
	flags.DurationVar(&parsed.expireInterval, "expire-interval", 100*time.Millisecond, "active expiration interval")
	if err := flags.Parse(arguments); err != nil {
		return options{}, err
	}
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
	return parsed, nil
}
