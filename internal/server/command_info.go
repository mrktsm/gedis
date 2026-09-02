package server

import (
	"fmt"
	"strings"

	"github.com/mrktsm/gedis/internal/resp"
)

func (e *Engine) handleCommand(arguments [][]byte) Result {
	if len(arguments) == 0 {
		return Result{Response: e.allCommandInfo()}
	}
	subcommand := strings.ToUpper(string(arguments[0]))
	switch subcommand {
	case "COUNT":
		if len(arguments) != 1 {
			return wrongArity("command|count")
		}
		return Result{Response: resp.Integer(int64(len(e.commands)))}
	case "INFO":
		if len(arguments) == 1 {
			return Result{Response: e.allCommandInfo()}
		}
		values := make([]resp.Value, 0, len(arguments)-1)
		for _, requested := range arguments[1:] {
			registered, exists := e.commands[strings.ToUpper(string(requested))]
			if !exists {
				values = append(values, resp.NullArray())
				continue
			}
			values = append(values, commandInfoValue(registered.metadata))
		}
		return Result{Response: resp.Array(values...)}
	case "HELP":
		if len(arguments) != 1 {
			return wrongArity("command|help")
		}
		return Result{Response: resp.Array(
			resp.BulkStringString("COMMAND"),
			resp.BulkStringString("COMMAND COUNT"),
			resp.BulkStringString("COMMAND INFO [command-name ...]"),
			resp.BulkStringString("COMMAND HELP"),
		)}
	default:
		safe := strings.NewReplacer("\r", "\\r", "\n", "\\n").Replace(string(arguments[0]))
		return Result{Response: resp.Error(fmt.Sprintf(
			"ERR unknown subcommand '%s'. Try COMMAND HELP.",
			safe,
		))}
	}
}

func (e *Engine) allCommandInfo() resp.Value {
	metadata := e.Commands()
	values := make([]resp.Value, 0, len(metadata))
	for _, command := range metadata {
		values = append(values, commandInfoValue(command))
	}
	return resp.Array(values...)
}

func commandInfoValue(metadata CommandMetadata) resp.Value {
	flags := make([]resp.Value, 0, len(metadata.Flags))
	for _, flag := range metadata.Flags {
		flags = append(flags, resp.SimpleString(flag))
	}
	return resp.Array(
		resp.BulkStringString(metadata.Name),
		resp.Integer(int64(metadata.Arity)),
		resp.Array(flags...),
		resp.Integer(int64(metadata.FirstKey)),
		resp.Integer(int64(metadata.LastKey)),
		resp.Integer(int64(metadata.KeyStep)),
	)
}
