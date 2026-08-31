package server

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/mrktsm/gedis/internal/resp"
	"github.com/mrktsm/gedis/internal/store"
)

type commandHandler func(arguments [][]byte) Result

type command struct {
	name    string
	handler commandHandler
}

// Result contains a command response and connection-level instructions.
type Result struct {
	Response resp.Value
	Close    bool
}

// Engine validates and dispatches commands independently of the network
// transport.
type Engine struct {
	commands map[string]command
	keyspace *store.Keyspace
}

func NewEngine() *Engine {
	return NewEngineWithStore(store.New())
}

func NewEngineWithStore(keyspace *store.Keyspace) *Engine {
	if keyspace == nil {
		keyspace = store.New()
	}
	engine := &Engine{
		commands: make(map[string]command),
		keyspace: keyspace,
	}
	engine.register("PING", handlePing)
	engine.register("ECHO", handleEcho)
	engine.register("QUIT", handleQuit)
	engine.register("GET", engine.handleGet)
	engine.register("SET", engine.handleSet)
	engine.register("DEL", engine.handleDelete)
	engine.register("EXISTS", engine.handleExists)
	engine.register("INCR", engine.handleIncrement)
	engine.register("INCRBY", engine.handleIncrementBy)
	engine.register("MGET", engine.handleMGet)
	engine.register("MSET", engine.handleMSet)
	engine.register("TYPE", engine.handleType)
	return engine
}

func (e *Engine) Execute(input [][]byte) Result {
	if len(input) == 0 || len(input[0]) == 0 {
		return Result{Response: resp.Error("ERR empty command")}
	}

	requestedName := string(input[0])
	registered, ok := e.commands[strings.ToUpper(requestedName)]
	if !ok {
		safeName := strings.NewReplacer("\r", "\\r", "\n", "\\n").Replace(requestedName)
		return Result{Response: resp.Error(fmt.Sprintf("ERR unknown command '%s'", safeName))}
	}
	return registered.handler(input[1:])
}

func (e *Engine) register(name string, handler commandHandler) {
	e.commands[name] = command{name: strings.ToLower(name), handler: handler}
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
	if options.ReturnPrevious {
		if result.PreviousExists {
			return Result{Response: resp.BulkString(result.Previous)}
		}
		return Result{Response: resp.NullBulkString()}
	}
	if !result.Applied {
		return Result{Response: resp.NullBulkString()}
	}
	return Result{Response: resp.SimpleString("OK")}
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
		case "EX", "PX":
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
			if option == "EX" {
				unit = time.Second
			}
			if amount <= 0 || amount > math.MaxInt64/int64(unit) {
				return store.SetOptions{}, fmt.Errorf("ERR invalid expire time in 'set' command")
			}
			options.TTL = time.Duration(amount) * unit
		default:
			return store.SetOptions{}, fmt.Errorf("ERR syntax error")
		}
	}
	return options, nil
}

func byteKeysToStrings(values [][]byte) []string {
	keys := make([]string, len(values))
	for index, value := range values {
		keys[index] = string(value)
	}
	return keys
}
