package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/mrktsm/gedis/internal/resp"
)

const defaultHealthTimeout = time.Second

type healthOptions struct {
	address string
	timeout time.Duration
}

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

func run(arguments []string, output io.Writer) int {
	options, err := parseHealthOptions(arguments, output)
	if err != nil {
		return 2
	}
	if err := checkHealth(options.address, options.timeout); err != nil {
		_, _ = fmt.Fprintf(output, "gedis health check failed: %v\n", err)
		return 1
	}
	return 0
}

func parseHealthOptions(arguments []string, output io.Writer) (healthOptions, error) {
	options := healthOptions{}
	flags := flag.NewFlagSet("gedis-healthcheck", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&options.address, "addr", "127.0.0.1:6379", "Gedis TCP address")
	flags.DurationVar(&options.timeout, "timeout", defaultHealthTimeout, "connect and command deadline")
	if err := flags.Parse(arguments); err != nil {
		return healthOptions{}, err
	}
	if flags.NArg() != 0 {
		return healthOptions{}, fmt.Errorf("unexpected positional arguments: %v", flags.Args())
	}
	if options.address == "" {
		return healthOptions{}, errors.New("addr cannot be empty")
	}
	if options.timeout <= 0 {
		return healthOptions{}, errors.New("timeout must be positive")
	}
	return options, nil
}

func checkHealth(address string, timeout time.Duration) error {
	connection, err := net.DialTimeout("tcp", address, timeout)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(timeout)); err != nil {
		return fmt.Errorf("set deadline: %w", err)
	}
	if err := resp.NewWriter(connection).WriteCommand([]byte("PING")); err != nil {
		return fmt.Errorf("write PING: %w", err)
	}
	response, err := resp.NewReader(connection).ReadValue()
	if err != nil {
		return fmt.Errorf("read PING: %w", err)
	}
	if response.Kind() != resp.KindSimpleString || string(response.Bytes()) != "PONG" {
		return fmt.Errorf("PING response is %q %q, want simple string PONG", response.Kind(), response.Bytes())
	}
	return nil
}
