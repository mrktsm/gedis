package server

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mrktsm/gedis/internal/resp"
	"github.com/mrktsm/gedis/internal/store"
)

type commandHandler func(arguments [][]byte) Result

type command struct {
	name    string
	write   bool
	handler commandHandler
}

type MutationSink interface {
	Append(command [][]byte) error
}

// Result contains a command response and connection-level instructions.
type Result struct {
	Response resp.Value
	Close    bool

	mutation     [][]byte
	skipMutation bool
}

// Engine validates and dispatches commands independently of the network
// transport.
type Engine struct {
	commands map[string]command
	keyspace *store.Keyspace

	writeMutex       sync.Mutex
	mutationSink     MutationSink
	persistenceError error
	readOnly         atomic.Bool
	replicationMutex sync.RWMutex
	replicationInfo  replicationInfoProvider

	rewriteMutex     sync.Mutex
	rewriteRunning   bool
	rewriteLastError error
	rewriteLastKeys  int
	rewriteCompleted int64
	rewriteWait      sync.WaitGroup
}

func NewEngine() *Engine {
	return NewEngineWithStore(store.New())
}

func NewEngineWithStore(keyspace *store.Keyspace) *Engine {
	return NewEngineWithStoreAndSink(keyspace, nil)
}

func NewEngineWithStoreAndSink(keyspace *store.Keyspace, sink MutationSink) *Engine {
	if keyspace == nil {
		keyspace = store.New()
	}
	engine := &Engine{
		commands:     make(map[string]command),
		keyspace:     keyspace,
		mutationSink: sink,
	}
	engine.register("PING", false, handlePing)
	engine.register("ECHO", false, handleEcho)
	engine.register("QUIT", false, handleQuit)
	engine.register("GET", false, engine.handleGet)
	engine.register("SET", true, engine.handleSet)
	engine.register("DEL", true, engine.handleDelete)
	engine.register("EXISTS", false, engine.handleExists)
	engine.register("INCR", true, engine.handleIncrement)
	engine.register("INCRBY", true, engine.handleIncrementBy)
	engine.register("MGET", false, engine.handleMGet)
	engine.register("MSET", true, engine.handleMSet)
	engine.register("TYPE", false, engine.handleType)
	engine.register("EXPIRE", true, engine.handleExpire)
	engine.register("PEXPIRE", true, engine.handlePExpire)
	engine.register("EXPIREAT", true, engine.handleExpireAt)
	engine.register("PEXPIREAT", true, engine.handlePExpireAt)
	engine.register("TTL", false, engine.handleTTL)
	engine.register("PTTL", false, engine.handlePTTL)
	engine.register("PERSIST", true, engine.handlePersist)
	engine.register("ZADD", true, engine.handleZAdd)
	engine.register("ZREM", true, engine.handleZRem)
	engine.register("ZSCORE", false, engine.handleZScore)
	engine.register("ZCARD", false, engine.handleZCard)
	engine.register("ZINCRBY", true, engine.handleZIncrBy)
	engine.register("ZRANGE", false, engine.handleZRange)
	engine.register("ZRANK", false, engine.handleZRank)
	engine.register("ZREVRANK", false, engine.handleZRevRank)
	engine.register("BGREWRITEAOF", false, engine.handleBGRewriteAOF)
	engine.register("INFO", false, engine.handleInfo)
	return engine
}

func (e *Engine) Execute(input [][]byte) Result {
	return e.execute(input, false)
}

// ApplyReplication executes an upstream command through the same mutation and
// persistence gate as client traffic while bypassing only replica read-only
// policy.
func (e *Engine) ApplyReplication(input [][]byte) Result {
	return e.execute(input, true)
}

func (e *Engine) execute(input [][]byte, replication bool) Result {
	if len(input) == 0 || len(input[0]) == 0 {
		return Result{Response: resp.Error("ERR empty command")}
	}

	requestedName := string(input[0])
	registered, ok := e.commands[strings.ToUpper(requestedName)]
	if !ok {
		safeName := strings.NewReplacer("\r", "\\r", "\n", "\\n").Replace(requestedName)
		return Result{Response: resp.Error(fmt.Sprintf("ERR unknown command '%s'", safeName))}
	}
	if !registered.write {
		return registered.handler(input[1:])
	}
	if e.readOnly.Load() && !replication {
		return Result{Response: resp.Error("READONLY You can't write against a read only replica.")}
	}

	e.writeMutex.Lock()
	defer e.writeMutex.Unlock()
	if e.persistenceError != nil {
		return persistenceFailure(e.persistenceError)
	}
	result := registered.handler(input[1:])
	if result.Response.Kind() == resp.KindError || result.skipMutation || e.mutationSink == nil {
		return result
	}
	mutation := input
	if result.mutation != nil {
		mutation = result.mutation
	}
	if err := e.mutationSink.Append(mutation); err != nil {
		e.persistenceError = err
		return persistenceFailure(err)
	}
	return result
}

// SetReadOnly controls client-facing write policy. Replication should apply
// upstream commands through a separate writable Engine sharing the keyspace.
func (e *Engine) SetReadOnly(readOnly bool) {
	e.readOnly.Store(readOnly)
}

func (e *Engine) ReadOnly() bool {
	return e.readOnly.Load()
}

func (e *Engine) SetReplicationInfoProvider(provider replicationInfoProvider) {
	e.replicationMutex.Lock()
	e.replicationInfo = provider
	e.replicationMutex.Unlock()
}

func (e *Engine) register(name string, write bool, handler commandHandler) {
	e.commands[name] = command{name: strings.ToLower(name), write: write, handler: handler}
}

func handlePing(arguments [][]byte) Result {
	switch len(arguments) {
	case 0:
		return Result{Response: resp.SimpleString("PONG")}
	case 1:
		return Result{Response: resp.BulkString(arguments[0])}
	default:
		return wrongArity("ping")
	}
}

func handleEcho(arguments [][]byte) Result {
	if len(arguments) != 1 {
		return wrongArity("echo")
	}
	return Result{Response: resp.BulkString(arguments[0])}
}

func handleQuit(arguments [][]byte) Result {
	if len(arguments) != 0 {
		return wrongArity("quit")
	}
	return Result{Response: resp.SimpleString("OK"), Close: true}
}

func wrongArity(commandName string) Result {
	message := fmt.Sprintf("ERR wrong number of arguments for '%s' command", commandName)
	return Result{Response: resp.Error(message)}
}

func (e *Engine) handleGet(arguments [][]byte) Result {
	if len(arguments) != 1 {
		return wrongArity("get")
	}
	value, exists, err := e.keyspace.Get(string(arguments[0]))
	if err != nil {
		return storeError(err)
	}
	if !exists {
		return Result{Response: resp.NullBulkString()}
	}
	return Result{Response: resp.BulkString(value)}
}

func (e *Engine) handleSet(arguments [][]byte) Result {
	if len(arguments) < 2 {
		return wrongArity("set")
	}

	options, parseError := parseSetOptions(arguments[2:])
	if parseError != nil {
		return Result{Response: resp.Error(parseError.Error())}
	}
	result, err := e.keyspace.Set(string(arguments[0]), arguments[1], options)
	if err != nil {
		return storeError(err)
	}
	commandResult := Result{}
	if result.Applied {
		commandResult.mutation = canonicalSetMutation(arguments[0], arguments[1], result.ExpiresAt)
	} else {
		commandResult.skipMutation = true
	}
	if options.ReturnPrevious {
		if result.PreviousExists {
			commandResult.Response = resp.BulkString(result.Previous)
			return commandResult
		}
		commandResult.Response = resp.NullBulkString()
		return commandResult
	}
	if !result.Applied {
		commandResult.Response = resp.NullBulkString()
		return commandResult
	}
	commandResult.Response = resp.SimpleString("OK")
	return commandResult
}

func (e *Engine) handleDelete(arguments [][]byte) Result {
	if len(arguments) == 0 {
		return wrongArity("del")
	}
	return Result{Response: resp.Integer(e.keyspace.Delete(byteKeysToStrings(arguments)...))}
}

func (e *Engine) handleExists(arguments [][]byte) Result {
	if len(arguments) == 0 {
		return wrongArity("exists")
	}
	return Result{Response: resp.Integer(e.keyspace.Exists(byteKeysToStrings(arguments)...))}
}

func (e *Engine) handleIncrement(arguments [][]byte) Result {
	if len(arguments) != 1 {
		return wrongArity("incr")
	}
	return e.increment(arguments[0], 1)
}

func (e *Engine) handleIncrementBy(arguments [][]byte) Result {
	if len(arguments) != 2 {
		return wrongArity("incrby")
	}
	increment, err := strconv.ParseInt(string(arguments[1]), 10, 64)
	if err != nil {
		return Result{Response: resp.Error("ERR value is not an integer or out of range")}
	}
	return e.increment(arguments[0], increment)
}

func (e *Engine) increment(key []byte, increment int64) Result {
	value, err := e.keyspace.Increment(string(key), increment)
	if err != nil {
		return storeError(err)
	}
	return Result{Response: resp.Integer(value)}
}

func (e *Engine) handleMGet(arguments [][]byte) Result {
	if len(arguments) == 0 {
		return wrongArity("mget")
	}
	results := e.keyspace.MGet(byteKeysToStrings(arguments)...)
	values := make([]resp.Value, len(results))
	for index, result := range results {
		if result.Exists {
			values[index] = resp.BulkString(result.Value)
		} else {
			values[index] = resp.NullBulkString()
		}
	}
	return Result{Response: resp.Array(values...)}
}

func (e *Engine) handleMSet(arguments [][]byte) Result {
	if len(arguments) == 0 || len(arguments)%2 != 0 {
		return wrongArity("mset")
	}
	pairs := make([]store.StringPair, 0, len(arguments)/2)
	for index := 0; index < len(arguments); index += 2 {
		pairs = append(pairs, store.StringPair{
			Key:   string(arguments[index]),
			Value: arguments[index+1],
		})
	}
	e.keyspace.MSet(pairs...)
	return Result{Response: resp.SimpleString("OK")}
}

func (e *Engine) handleType(arguments [][]byte) Result {
	if len(arguments) != 1 {
		return wrongArity("type")
	}
	kind, exists := e.keyspace.Kind(string(arguments[0]))
	if !exists {
		return Result{Response: resp.SimpleString("none")}
	}
	switch kind {
	case store.KindString:
		return Result{Response: resp.SimpleString("string")}
	case store.KindSortedSet:
		return Result{Response: resp.SimpleString("zset")}
	default:
		return Result{Response: resp.SimpleString("none")}
	}
}

func (e *Engine) handleExpire(arguments [][]byte) Result {
	if len(arguments) != 2 {
		return wrongArity("expire")
	}
	return e.expire(arguments, time.Second)
}

func (e *Engine) handlePExpire(arguments [][]byte) Result {
	if len(arguments) != 2 {
		return wrongArity("pexpire")
	}
	return e.expire(arguments, time.Millisecond)
}

func (e *Engine) expire(arguments [][]byte, unit time.Duration) Result {
	amount, err := strconv.ParseInt(string(arguments[1]), 10, 64)
	if err != nil || amount > math.MaxInt64/int64(unit) || amount < math.MinInt64/int64(unit) {
		return Result{Response: resp.Error("ERR value is not an integer or out of range")}
	}
	applied, expiresAt := e.keyspace.Expire(string(arguments[0]), time.Duration(amount)*unit)
	if applied {
		result := Result{Response: resp.Integer(1)}
		if expiresAt.IsZero() {
			result.mutation = [][]byte{[]byte("DEL"), arguments[0]}
		} else {
			result.mutation = canonicalExpireAtMutation(arguments[0], expiresAt)
		}
		return result
	}
	return Result{Response: resp.Integer(0), skipMutation: true}
}

func (e *Engine) handleExpireAt(arguments [][]byte) Result {
	if len(arguments) != 2 {
		return wrongArity("expireat")
	}
	seconds, err := strconv.ParseInt(string(arguments[1]), 10, 64)
	if err != nil {
		return Result{Response: resp.Error("ERR value is not an integer or out of range")}
	}
	return e.expireAt(arguments[0], time.Unix(seconds, 0))
}

func (e *Engine) handlePExpireAt(arguments [][]byte) Result {
	if len(arguments) != 2 {
		return wrongArity("pexpireat")
	}
	milliseconds, err := strconv.ParseInt(string(arguments[1]), 10, 64)
	if err != nil {
		return Result{Response: resp.Error("ERR value is not an integer or out of range")}
	}
	return e.expireAt(arguments[0], time.UnixMilli(milliseconds))
}

func (e *Engine) expireAt(key []byte, expiresAt time.Time) Result {
	applied, storedDeadline := e.keyspace.ExpireAt(string(key), expiresAt)
	if !applied {
		return Result{Response: resp.Integer(0), skipMutation: true}
	}
	result := Result{Response: resp.Integer(1)}
	if storedDeadline.IsZero() {
		result.mutation = [][]byte{[]byte("DEL"), key}
	} else {
		result.mutation = canonicalExpireAtMutation(key, storedDeadline)
	}
	return result
}

func (e *Engine) handleTTL(arguments [][]byte) Result {
	if len(arguments) != 1 {
		return wrongArity("ttl")
	}
	return Result{Response: resp.Integer(e.keyspace.TTL(string(arguments[0])))}
}

func (e *Engine) handlePTTL(arguments [][]byte) Result {
	if len(arguments) != 1 {
		return wrongArity("pttl")
	}
	return Result{Response: resp.Integer(e.keyspace.PTTL(string(arguments[0])))}
}

func (e *Engine) handlePersist(arguments [][]byte) Result {
	if len(arguments) != 1 {
		return wrongArity("persist")
	}
	if e.keyspace.Persist(string(arguments[0])) {
		return Result{Response: resp.Integer(1)}
	}
	return Result{Response: resp.Integer(0)}
}

func storeError(err error) Result {
	switch err {
	case store.ErrWrongType:
		return Result{Response: resp.Error(
			"WRONGTYPE Operation against a key holding the wrong kind of value",
		)}
	case store.ErrNotInteger:
		return Result{Response: resp.Error("ERR value is not an integer or out of range")}
	default:
		return Result{Response: resp.Error("ERR internal server error")}
	}
}

func parseSetOptions(arguments [][]byte) (store.SetOptions, error) {
	options := store.SetOptions{}
	conditionSet := false
	expirationSet := false
	getSet := false

	for index := 0; index < len(arguments); index++ {
		option := strings.ToUpper(string(arguments[index]))
		switch option {
		case "NX", "XX":
			if conditionSet {
				return store.SetOptions{}, fmt.Errorf("ERR syntax error")
			}
			conditionSet = true
			if option == "NX" {
				options.Condition = store.SetIfAbsent
			} else {
				options.Condition = store.SetIfPresent
			}
		case "GET":
			if getSet {
				return store.SetOptions{}, fmt.Errorf("ERR syntax error")
			}
			getSet = true
			options.ReturnPrevious = true
		case "KEEPTTL":
			if expirationSet {
				return store.SetOptions{}, fmt.Errorf("ERR syntax error")
			}
			expirationSet = true
			options.KeepTTL = true
		case "EX", "PX", "EXAT", "PXAT":
			if expirationSet || index+1 >= len(arguments) {
				return store.SetOptions{}, fmt.Errorf("ERR syntax error")
			}
			expirationSet = true
			index++
			amount, err := strconv.ParseInt(string(arguments[index]), 10, 64)
			if err != nil {
				return store.SetOptions{}, fmt.Errorf("ERR value is not an integer or out of range")
			}
			unit := time.Millisecond
			if option == "EX" || option == "EXAT" {
				unit = time.Second
			}
			if amount <= 0 || (option == "EX" || option == "PX") && amount > math.MaxInt64/int64(unit) {
				return store.SetOptions{}, fmt.Errorf("ERR invalid expire time in 'set' command")
			}
			if option == "EX" || option == "PX" {
				options.TTL = time.Duration(amount) * unit
			} else if option == "EXAT" {
				options.ExpiresAt = time.Unix(amount, 0)
			} else {
				options.ExpiresAt = time.UnixMilli(amount)
			}
		default:
			return store.SetOptions{}, fmt.Errorf("ERR syntax error")
		}
	}
	return options, nil
}

func canonicalSetMutation(key, value []byte, expiresAt time.Time) [][]byte {
	mutation := [][]byte{[]byte("SET"), key, value}
	if !expiresAt.IsZero() {
		mutation = append(mutation, []byte("PXAT"), []byte(strconv.FormatInt(expiresAt.UnixMilli(), 10)))
	}
	return mutation
}

func canonicalExpireAtMutation(key []byte, expiresAt time.Time) [][]byte {
	return [][]byte{
		[]byte("PEXPIREAT"),
		key,
		[]byte(strconv.FormatInt(expiresAt.UnixMilli(), 10)),
	}
}

func persistenceFailure(err error) Result {
	safeMessage := strings.NewReplacer("\r", "\\r", "\n", "\\n").Replace(err.Error())
	return Result{Response: resp.Error("MISCONF Errors writing to the append-only file: " + safeMessage)}
}

func byteKeysToStrings(values [][]byte) []string {
	keys := make([]string, len(values))
	for index, value := range values {
		keys[index] = string(value)
	}
	return keys
}
