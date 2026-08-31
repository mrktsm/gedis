package server

import (
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/mrktsm/gedis/internal/resp"
	"github.com/mrktsm/gedis/internal/store"
)

type zAddCommandOptions struct {
	storeOptions store.ZAddOptions
	changed      bool
}

func (e *Engine) handleZAdd(arguments [][]byte) Result {
	if len(arguments) < 3 {
		return wrongArity("zadd")
	}

	options, pairStart, parseError := parseZAddOptions(arguments)
	if parseError != nil {
		return Result{Response: resp.Error(parseError.Error())}
	}
	pairArguments := arguments[pairStart:]
	if len(pairArguments) == 0 || len(pairArguments)%2 != 0 {
		return Result{Response: resp.Error("ERR syntax error")}
	}
	if options.storeOptions.Increment && len(pairArguments) != 2 {
		return Result{Response: resp.Error("ERR INCR option supports a single increment-element pair")}
	}

	updates := make([]store.ZUpdate, 0, len(pairArguments)/2)
	for index := 0; index < len(pairArguments); index += 2 {
		score, err := parseScore(pairArguments[index])
		if err != nil {
			return Result{Response: resp.Error("ERR value is not a valid float")}
		}
		updates = append(updates, store.ZUpdate{
			Score:  score,
			Member: string(pairArguments[index+1]),
		})
	}

	result, err := e.keyspace.ZAdd(string(arguments[0]), updates, options.storeOptions)
	if err != nil {
		if errors.Is(err, store.ErrNotFloat) {
			return Result{Response: resp.Error("ERR resulting score is not a number (NaN)")}
		}
		return storeError(err)
	}
	if options.storeOptions.Increment {
		if !result.Applied {
			return Result{Response: resp.NullBulkString()}
		}
		return Result{Response: resp.BulkStringString(formatScore(result.Score))}
	}
	count := result.Added
	if options.changed {
		count += result.Updated
	}
	return Result{Response: resp.Integer(count)}
}

func (e *Engine) handleZRem(arguments [][]byte) Result {
	if len(arguments) < 2 {
		return wrongArity("zrem")
	}
	members := byteKeysToStrings(arguments[1:])
	removed, err := e.keyspace.ZRem(string(arguments[0]), members...)
	if err != nil {
		return storeError(err)
	}
	return Result{Response: resp.Integer(removed)}
}

func (e *Engine) handleZScore(arguments [][]byte) Result {
	if len(arguments) != 2 {
		return wrongArity("zscore")
	}
	score, exists, err := e.keyspace.ZScore(string(arguments[0]), string(arguments[1]))
	if err != nil {
		return storeError(err)
	}
	if !exists {
		return Result{Response: resp.NullBulkString()}
	}
	return Result{Response: resp.BulkStringString(formatScore(score))}
}

func (e *Engine) handleZCard(arguments [][]byte) Result {
	if len(arguments) != 1 {
		return wrongArity("zcard")
	}
	size, err := e.keyspace.ZCard(string(arguments[0]))
	if err != nil {
		return storeError(err)
	}
	return Result{Response: resp.Integer(size)}
}

func (e *Engine) handleZIncrBy(arguments [][]byte) Result {
	if len(arguments) != 3 {
		return wrongArity("zincrby")
	}
	increment, err := parseScore(arguments[1])
	if err != nil {
		return Result{Response: resp.Error("ERR value is not a valid float")}
	}
	score, _, err := e.keyspace.ZIncrBy(string(arguments[0]), string(arguments[2]), increment)
	if err != nil {
		if errors.Is(err, store.ErrNotFloat) {
			return Result{Response: resp.Error("ERR resulting score is not a number (NaN)")}
		}
		return storeError(err)
	}
	return Result{Response: resp.BulkStringString(formatScore(score))}
}

func (e *Engine) handleZRank(arguments [][]byte) Result {
	return e.zRank(arguments, false, "zrank")
}

func (e *Engine) handleZRevRank(arguments [][]byte) Result {
	return e.zRank(arguments, true, "zrevrank")
}

func (e *Engine) zRank(arguments [][]byte, reverse bool, commandName string) Result {
	if len(arguments) != 2 {
		return wrongArity(commandName)
	}
	rank, exists, err := e.keyspace.ZRank(string(arguments[0]), string(arguments[1]), reverse)
	if err != nil {
		return storeError(err)
	}
	if !exists {
		return Result{Response: resp.NullBulkString()}
	}
	return Result{Response: resp.Integer(rank)}
}

type zRangeOptions struct {
	byScore    bool
	reverse    bool
	withScores bool
	hasLimit   bool
	offset     int64
	count      int64
}

func (e *Engine) handleZRange(arguments [][]byte) Result {
	if len(arguments) < 3 {
		return wrongArity("zrange")
	}
	options, parseError := parseZRangeOptions(arguments[3:])
	if parseError != nil {
		return Result{Response: resp.Error(parseError.Error())}
	}

	var items []store.ZItem
	var err error
	if options.byScore {
		first, firstError := parseScoreBoundary(arguments[1])
		second, secondError := parseScoreBoundary(arguments[2])
		if firstError != nil || secondError != nil {
			return Result{Response: resp.Error("ERR min or max is not a float")}
		}
		minimum, maximum := first, second
		if options.reverse {
			minimum, maximum = second, first
		}
		count := int64(-1)
		if options.hasLimit {
			count = options.count
		}
		items, err = e.keyspace.ZRangeByScore(
			string(arguments[0]),
			minimum,
			maximum,
			options.reverse,
			options.offset,
			count,
		)
	} else {
		start, startError := strconv.ParseInt(string(arguments[1]), 10, 64)
		stop, stopError := strconv.ParseInt(string(arguments[2]), 10, 64)
		if startError != nil || stopError != nil {
			return Result{Response: resp.Error("ERR value is not an integer or out of range")}
		}
		items, err = e.keyspace.ZRangeByRank(string(arguments[0]), start, stop, options.reverse)
	}
	if err != nil {
		return storeError(err)
	}
	return Result{Response: encodeZItems(items, options.withScores)}
}

func parseZAddOptions(arguments [][]byte) (zAddCommandOptions, int, error) {
	options := zAddCommandOptions{}
	nx := false
	xx := false
	greater := false
	less := false

	index := 1
	for index < len(arguments) {
		switch strings.ToUpper(string(arguments[index])) {
		case "NX":
			nx = true
			index++
		case "XX":
			xx = true
			index++
		case "GT":
			greater = true
			index++
		case "LT":
			less = true
			index++
		case "CH":
			options.changed = true
			index++
		case "INCR":
			options.storeOptions.Increment = true
			index++
		default:
			if nx && xx {
				return zAddCommandOptions{}, 0, errors.New("ERR XX and NX options at the same time are not compatible")
			}
			if (nx && (greater || less)) || (greater && less) {
				return zAddCommandOptions{}, 0, errors.New("ERR GT, LT, and/or NX options at the same time are not compatible")
			}
			if nx {
				options.storeOptions.Condition = store.ZAddIfAbsent
			} else if xx {
				options.storeOptions.Condition = store.ZAddIfPresent
			}
			if greater {
				options.storeOptions.Comparison = store.ZAddIfGreater
			} else if less {
				options.storeOptions.Comparison = store.ZAddIfLess
			}
			return options, index, nil
		}
	}
	return options, index, nil
}

func parseZRangeOptions(arguments [][]byte) (zRangeOptions, error) {
	options := zRangeOptions{}
	for index := 0; index < len(arguments); index++ {
		switch strings.ToUpper(string(arguments[index])) {
		case "BYSCORE":
			if options.byScore {
				return zRangeOptions{}, errors.New("ERR syntax error")
			}
			options.byScore = true
		case "REV":
			if options.reverse {
				return zRangeOptions{}, errors.New("ERR syntax error")
			}
			options.reverse = true
		case "WITHSCORES":
			if options.withScores {
				return zRangeOptions{}, errors.New("ERR syntax error")
			}
			options.withScores = true
		case "LIMIT":
			if options.hasLimit || index+2 >= len(arguments) {
				return zRangeOptions{}, errors.New("ERR syntax error")
			}
			options.hasLimit = true
			index++
			offset, offsetError := strconv.ParseInt(string(arguments[index]), 10, 64)
			index++
			count, countError := strconv.ParseInt(string(arguments[index]), 10, 64)
			if offsetError != nil || countError != nil {
				return zRangeOptions{}, errors.New("ERR value is not an integer or out of range")
			}
			options.offset = offset
			options.count = count
		default:
			return zRangeOptions{}, errors.New("ERR syntax error")
		}
	}
	if options.hasLimit && !options.byScore {
		return zRangeOptions{}, errors.New("ERR syntax error, LIMIT is only supported in combination with either BYSCORE or BYLEX")
	}
	return options, nil
}

func parseScore(value []byte) (float64, error) {
	score, err := strconv.ParseFloat(string(value), 64)
	if err != nil || math.IsNaN(score) {
		return 0, errors.New("invalid float")
	}
	return score, nil
}

func parseScoreBoundary(value []byte) (store.ScoreBoundary, error) {
	boundary := store.ScoreBoundary{}
	if len(value) > 0 && value[0] == '(' {
		boundary.Exclusive = true
		value = value[1:]
	}
	score, err := parseScore(value)
	if err != nil {
		return store.ScoreBoundary{}, err
	}
	boundary.Value = score
	return boundary, nil
}

func formatScore(score float64) string {
	switch {
	case math.IsInf(score, 1):
		return "inf"
	case math.IsInf(score, -1):
		return "-inf"
	default:
		return strconv.FormatFloat(score, 'g', -1, 64)
	}
}

func encodeZItems(items []store.ZItem, withScores bool) resp.Value {
	if len(items) == 0 {
		return resp.Array()
	}
	capacity := len(items)
	if withScores {
		capacity *= 2
	}
	values := make([]resp.Value, 0, capacity)
	for _, item := range items {
		values = append(values, resp.BulkString([]byte(item.Member)))
		if withScores {
			values = append(values, resp.BulkStringString(formatScore(item.Score)))
		}
	}
	return resp.Array(values...)
}
