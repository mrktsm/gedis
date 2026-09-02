package server

import (
	"sort"
	"strings"
)

type CommandMetadata struct {
	Name     string
	Arity    int
	Write    bool
	FirstKey int
	LastKey  int
	KeyStep  int
	Flags    []string
	Group    string
	Syntax   string
}

func (e *Engine) registerCommands() {
	e.register(describeCommand("PING", -1, false, 0, 0, 0, "connection", "PING [message]", "fast"), handlePing)
	e.register(describeCommand("ECHO", 2, false, 0, 0, 0, "connection", "ECHO message", "fast"), handleEcho)
	e.register(describeCommand("QUIT", 1, false, 0, 0, 0, "connection", "QUIT", "fast"), handleQuit)
	e.register(describeCommand("GET", 2, false, 1, 1, 1, "string", "GET key", "readonly", "fast"), e.handleGet)
	e.register(describeCommand("SET", -3, true, 1, 1, 1, "string", "SET key value [NX|XX] [GET] [EX seconds|PX milliseconds|EXAT unix-seconds|PXAT unix-milliseconds|KEEPTTL]", "write"), e.handleSet)
	e.register(describeCommand("DEL", -2, true, 1, -1, 1, "generic", "DEL key [key ...]", "write"), e.handleDelete)
	e.register(describeCommand("EXISTS", -2, false, 1, -1, 1, "generic", "EXISTS key [key ...]", "readonly", "fast"), e.handleExists)
	e.register(describeCommand("INCR", 2, true, 1, 1, 1, "string", "INCR key", "write", "fast"), e.handleIncrement)
	e.register(describeCommand("INCRBY", 3, true, 1, 1, 1, "string", "INCRBY key increment", "write", "fast"), e.handleIncrementBy)
	e.register(describeCommand("MGET", -2, false, 1, -1, 1, "string", "MGET key [key ...]", "readonly", "fast"), e.handleMGet)
	e.register(describeCommand("MSET", -3, true, 1, -1, 2, "string", "MSET key value [key value ...]", "write"), e.handleMSet)
	e.register(describeCommand("TYPE", 2, false, 1, 1, 1, "generic", "TYPE key", "readonly", "fast"), e.handleType)
	e.register(describeCommand("EXPIRE", 3, true, 1, 1, 1, "expiration", "EXPIRE key seconds", "write", "fast"), e.handleExpire)
	e.register(describeCommand("PEXPIRE", 3, true, 1, 1, 1, "expiration", "PEXPIRE key milliseconds", "write", "fast"), e.handlePExpire)
	e.register(describeCommand("EXPIREAT", 3, true, 1, 1, 1, "expiration", "EXPIREAT key unix-time-seconds", "write", "fast"), e.handleExpireAt)
	e.register(describeCommand("PEXPIREAT", 3, true, 1, 1, 1, "expiration", "PEXPIREAT key unix-time-milliseconds", "write", "fast"), e.handlePExpireAt)
	e.register(describeCommand("TTL", 2, false, 1, 1, 1, "expiration", "TTL key", "readonly", "fast"), e.handleTTL)
	e.register(describeCommand("PTTL", 2, false, 1, 1, 1, "expiration", "PTTL key", "readonly", "fast"), e.handlePTTL)
	e.register(describeCommand("PERSIST", 2, true, 1, 1, 1, "expiration", "PERSIST key", "write", "fast"), e.handlePersist)
	e.register(describeCommand("ZADD", -4, true, 1, 1, 1, "sorted-set", "ZADD key [NX|XX] [GT|LT] [CH] [INCR] score member [score member ...]", "write", "fast"), e.handleZAdd)
	e.register(describeCommand("ZREM", -3, true, 1, 1, 1, "sorted-set", "ZREM key member [member ...]", "write", "fast"), e.handleZRem)
	e.register(describeCommand("ZSCORE", 3, false, 1, 1, 1, "sorted-set", "ZSCORE key member", "readonly", "fast"), e.handleZScore)
	e.register(describeCommand("ZCARD", 2, false, 1, 1, 1, "sorted-set", "ZCARD key", "readonly", "fast"), e.handleZCard)
	e.register(describeCommand("ZINCRBY", 4, true, 1, 1, 1, "sorted-set", "ZINCRBY key increment member", "write", "fast"), e.handleZIncrBy)
	e.register(describeCommand("ZRANGE", -4, false, 1, 1, 1, "sorted-set", "ZRANGE key start stop [BYSCORE] [REV] [LIMIT offset count] [WITHSCORES]", "readonly"), e.handleZRange)
	e.register(describeCommand("ZRANK", 3, false, 1, 1, 1, "sorted-set", "ZRANK key member", "readonly", "fast"), e.handleZRank)
	e.register(describeCommand("ZREVRANK", 3, false, 1, 1, 1, "sorted-set", "ZREVRANK key member", "readonly", "fast"), e.handleZRevRank)
	e.register(describeCommand("BGREWRITEAOF", 1, false, 0, 0, 0, "persistence", "BGREWRITEAOF", "admin"), e.handleBGRewriteAOF)
	e.register(describeCommand("INFO", -1, false, 0, 0, 0, "server", "INFO [section ...]"), e.handleInfo)
	e.register(describeCommand("COMMAND", -1, false, 0, 0, 0, "server", "COMMAND [COUNT|INFO [command-name ...]|HELP]"), e.handleCommand)
}

func (e *Engine) register(metadata CommandMetadata, handler commandHandler) {
	e.commands[strings.ToUpper(metadata.Name)] = command{metadata: metadata, handler: handler}
}

func describeCommand(
	name string,
	arity int,
	write bool,
	firstKey int,
	lastKey int,
	keyStep int,
	group string,
	syntax string,
	flags ...string,
) CommandMetadata {
	return CommandMetadata{
		Name:     strings.ToLower(name),
		Arity:    arity,
		Write:    write,
		FirstKey: firstKey,
		LastKey:  lastKey,
		KeyStep:  keyStep,
		Flags:    append([]string(nil), flags...),
		Group:    group,
		Syntax:   syntax,
	}
}

func (e *Engine) Commands() []CommandMetadata {
	commands := make([]CommandMetadata, 0, len(e.commands))
	for _, registered := range e.commands {
		metadata := registered.metadata
		metadata.Flags = append([]string(nil), metadata.Flags...)
		commands = append(commands, metadata)
	}
	sort.Slice(commands, func(first, second int) bool {
		return commands[first].Name < commands[second].Name
	})
	return commands
}
