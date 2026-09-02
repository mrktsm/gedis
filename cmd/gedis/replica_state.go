package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/mrktsm/gedis/internal/aof"
	"github.com/mrktsm/gedis/internal/replication"
)

func resolveReplicaStatePath(options options) string {
	if options.replicaOf == "" || !options.appendOnly {
		return ""
	}
	if options.replicaStatePath != "" {
		return options.replicaStatePath
	}
	return options.aofPath + ".replstate"
}

func loadReplicaState(path, primaryAddress string, aofSize int64) (*replication.PersistentState, error) {
	state, err := replication.LoadState(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if state.PrimaryAddress != primaryAddress {
		return nil, fmt.Errorf(
			"checkpoint primary %q does not match configured primary %q",
			state.PrimaryAddress,
			primaryAddress,
		)
	}
	if state.AOFSize != aofSize {
		return nil, fmt.Errorf(
			"checkpoint AOF size %d does not match recovered AOF size %d",
			state.AOFSize,
			aofSize,
		)
	}
	return &state, nil
}

func saveReplicaState(path string, replica *replication.Replica, appendLog *aof.Log) error {
	if replica == nil {
		return errors.New("replica is required")
	}
	if appendLog == nil {
		return errors.New("append-only log is required")
	}
	if err := appendLog.Sync(); err != nil {
		return err
	}
	enabled, _, aofSize, err := appendLog.AOFInfo()
	if err != nil {
		return err
	}
	if !enabled {
		return errors.New("append-only persistence is disabled")
	}
	state, err := replica.Checkpoint(aofSize)
	if err != nil {
		return err
	}
	return replication.SaveState(path, state)
}
