package aof

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/mrktsm/gedis/internal/resp"
)

type ReplayError struct {
	Offset    int64
	Truncated bool
	Err       error
}

func (e *ReplayError) Error() string {
	return fmt.Sprintf("aof: replay failed at byte %d: %v", e.Offset, e.Err)
}

func (e *ReplayError) Unwrap() error {
	return e.Err
}

type ReplayResult struct {
	Commands   int64
	ValidBytes int64
}

// ReplayFile applies complete commands in order. A missing file is treated as
// an empty log. The callback must not append the replayed command back to the
// same log.
func ReplayFile(path string, apply func(command [][]byte) error) (ReplayResult, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return ReplayResult{}, nil
	}
	if err != nil {
		return ReplayResult{}, fmt.Errorf("aof: open for replay: %w", err)
	}
	defer file.Close()
	return Replay(file, apply)
}

func Replay(reader io.Reader, apply func(command [][]byte) error) (ReplayResult, error) {
	decoder := resp.NewReader(reader)
	result := ReplayResult{}
	for {
		command, err := decoder.ReadCommand()
		if errors.Is(err, io.EOF) {
			return result, nil
		}
		if err != nil {
			return result, &ReplayError{
				Offset:    result.ValidBytes,
				Truncated: errors.Is(err, io.ErrUnexpectedEOF),
				Err:       err,
			}
		}
		if apply != nil {
			if err := apply(command); err != nil {
				return result, &ReplayError{Offset: result.ValidBytes, Err: err}
			}
		}
		encoded, err := encodeCommand(command)
		if err != nil {
			return result, &ReplayError{Offset: result.ValidBytes, Err: err}
		}
		result.Commands++
		result.ValidBytes += int64(len(encoded))
	}
}

func RepairTruncatedFile(path string, replayError *ReplayError) error {
	if replayError == nil || !replayError.Truncated {
		return errors.New("aof: repair requires a truncated-tail replay error")
	}
	if err := os.Truncate(path, replayError.Offset); err != nil {
		return fmt.Errorf("aof: truncate to byte %d: %w", replayError.Offset, err)
	}
	return nil
}
