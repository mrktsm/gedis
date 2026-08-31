package main

import (
	"errors"
	"fmt"

	"github.com/mrktsm/gedis/internal/aof"
	"github.com/mrktsm/gedis/internal/resp"
	"github.com/mrktsm/gedis/internal/server"
	"github.com/mrktsm/gedis/internal/store"
)

// recoverAOF applies the valid command prefix before the server starts
// accepting connections. A truncated final command is repaired only when the
// operator explicitly opts in; malformed complete commands always fail.
func recoverAOF(path string, keyspace *store.Keyspace, repairTruncated bool) (aof.ReplayResult, bool, error) {
	engine := server.NewEngineWithStore(keyspace)
	result, err := aof.ReplayFile(path, func(command [][]byte) error {
		response := engine.Execute(command).Response
		if response.Kind() == resp.KindError {
			return fmt.Errorf("command returned %s", response.Bytes())
		}
		return nil
	})
	if err == nil {
		return result, false, nil
	}

	var replayError *aof.ReplayError
	if !repairTruncated || !errors.As(err, &replayError) || !replayError.Truncated {
		return result, false, err
	}
	if err := aof.RepairTruncatedFile(path, replayError); err != nil {
		return result, false, err
	}
	return result, true, nil
}
