package server

import (
	"fmt"
	"strings"

	"github.com/mrktsm/gedis/internal/resp"
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
}

func NewEngine() *Engine {
	engine := &Engine{commands: make(map[string]command)}
	engine.register("PING", handlePing)
	engine.register("ECHO", handleEcho)
	engine.register("QUIT", handleQuit)
	return engine
}

func (e *Engine) Execute(input [][]byte) Result {
	if len(input) == 0 || len(input[0]) == 0 {
		return Result{Response: resp.Error("ERR empty command")}
	}

	requestedName := string(input[0])
	registered, ok := e.commands[strings.ToUpper(requestedName)]
	if !ok {
		return Result{Response: resp.Error(fmt.Sprintf("ERR unknown command '%s'", requestedName))}
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
