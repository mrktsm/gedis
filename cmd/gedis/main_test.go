package main

import (
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/mrktsm/gedis/internal/resp"
)

func TestParseOptionsDefaults(t *testing.T) {
	t.Parallel()

	got, err := parseOptions(nil, io.Discard)
	if err != nil {
		t.Fatalf("parseOptions() error = %v", err)
	}
	want := options{
		address:        "127.0.0.1:6379",
		writeTimeout:   5 * time.Second,
		maxBulkLength:  resp.DefaultLimits.MaxBulkLength,
		maxArrayLength: resp.DefaultLimits.MaxArrayLength,
		expireInterval: 100 * time.Millisecond,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseOptions() = %#v, want %#v", got, want)
	}
}

func TestParseOptionsOverrides(t *testing.T) {
	t.Parallel()

	got, err := parseOptions([]string{
		"-addr", "localhost:1234",
		"-read-timeout", "2s",
		"-write-timeout", "3s",
		"-max-bulk-bytes", "2048",
		"-max-array-length", "32",
		"-expire-interval", "250ms",
	}, io.Discard)
	if err != nil {
		t.Fatalf("parseOptions() error = %v", err)
	}
	want := options{
		address:        "localhost:1234",
		readTimeout:    2 * time.Second,
		writeTimeout:   3 * time.Second,
		maxBulkLength:  2048,
		maxArrayLength: 32,
		expireInterval: 250 * time.Millisecond,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseOptions() = %#v, want %#v", got, want)
	}
}

func TestParseOptionsRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	for _, arguments := range [][]string{
		{"positional"},
		{"-addr", ""},
		{"-read-timeout", "-1s"},
		{"-write-timeout", "-1s"},
		{"-max-bulk-bytes", "0"},
		{"-max-array-length", "-1"},
		{"-expire-interval", "0"},
	} {
		if _, err := parseOptions(arguments, io.Discard); err == nil {
			t.Fatalf("parseOptions(%q) error = nil, want error", arguments)
		}
	}
}
