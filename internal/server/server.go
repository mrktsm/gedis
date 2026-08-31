package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/mrktsm/gedis/internal/resp"
)

const outputBufferSize = 16 * 1024

// Config controls network behavior and protocol resource limits.
type Config struct {
	ReadTimeout              time.Duration
	WriteTimeout             time.Duration
	ProtocolLimits           resp.Limits
	ConnectionCommandHandler ConnectionCommandHandler
}

// ConnectionCommandHandler can intercept commands that change a connection's
// protocol mode, such as PSYNC. A terminal handler owns the connection until it
// returns; ordinary commands continue through Engine.
type ConnectionCommandHandler interface {
	HandleConnectionCommand(
		ctx context.Context,
		command [][]byte,
		connection net.Conn,
		writer *bufio.Writer,
	) (handled bool, terminal bool, err error)
}

func DefaultConfig() Config {
	return Config{ProtocolLimits: resp.DefaultLimits}
}

// Server accepts RESP2 commands and dispatches them to an Engine.
type Server struct {
	config Config
	engine *Engine

	mutex       sync.Mutex
	listener    net.Listener
	connections map[net.Conn]struct{}
	closing     bool
	waitGroup   sync.WaitGroup
	lifecycle   context.Context
	stop        context.CancelFunc
}

func New(config Config, engine *Engine) *Server {
	if engine == nil {
		engine = NewEngine()
	}
	lifecycle, stop := context.WithCancel(context.Background())
	return &Server{
		config:      config,
		engine:      engine,
		connections: make(map[net.Conn]struct{}),
		lifecycle:   lifecycle,
		stop:        stop,
	}
}

// Serve accepts connections until Shutdown is called or the listener fails.
// An expected shutdown returns nil.
func (s *Server) Serve(listener net.Listener) error {
	if listener == nil {
		return errors.New("server: nil listener")
	}

	s.mutex.Lock()
	if s.closing {
		s.mutex.Unlock()
		return errors.New("server: cannot serve after shutdown")
	}
	if s.listener != nil {
		s.mutex.Unlock()
		return errors.New("server: already serving")
	}
	s.listener = listener
	s.mutex.Unlock()

	var retryDelay time.Duration
	for {
		connection, err := listener.Accept()
		if err != nil {
			if s.isClosing() {
				return nil
			}

			var networkError net.Error
			if errors.As(err, &networkError) && networkError.Temporary() {
				if retryDelay == 0 {
					retryDelay = 5 * time.Millisecond
				} else {
					retryDelay *= 2
				}
				if maximum := time.Second; retryDelay > maximum {
					retryDelay = maximum
				}
				time.Sleep(retryDelay)
				continue
			}
			return fmt.Errorf("server: accept connection: %w", err)
		}
		retryDelay = 0

		if !s.trackConnection(connection) {
			_ = connection.Close()
			continue
		}
		go s.serveConnection(connection)
	}
}

// Shutdown stops accepting clients, closes active connections to unblock their
// read loops, and waits for connection goroutines to finish.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mutex.Lock()
	if !s.closing {
		s.closing = true
	}
	listener := s.listener
	connections := make([]net.Conn, 0, len(s.connections))
	for connection := range s.connections {
		connections = append(connections, connection)
	}
	s.mutex.Unlock()

	s.stop()
	if listener != nil {
		_ = listener.Close()
	}
	for _, connection := range connections {
		_ = connection.Close()
	}

	done := make(chan struct{})
	go func() {
		s.waitGroup.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Server) serveConnection(connection net.Conn) {
	defer func() {
		_ = connection.Close()
		s.untrackConnection(connection)
	}()

	reader := resp.NewReaderWithLimits(connection, s.config.ProtocolLimits)
	bufferedWriter := bufio.NewWriterSize(connection, outputBufferSize)
	writer := resp.NewWriter(bufferedWriter)

	for {
		if s.config.ReadTimeout > 0 {
			_ = connection.SetReadDeadline(time.Now().Add(s.config.ReadTimeout))
		}
		command, err := reader.ReadCommand()
		if err != nil {
			var protocolError *resp.ProtocolError
			if errors.As(err, &protocolError) {
				s.writeResponse(connection, bufferedWriter, writer, resp.Error(
					"ERR Protocol error: "+protocolError.Message(),
				))
			}
			return
		}
		if handler := s.config.ConnectionCommandHandler; handler != nil {
			handled, terminal, err := handler.HandleConnectionCommand(
				s.lifecycle,
				command,
				connection,
				bufferedWriter,
			)
			if err != nil {
				return
			}
			if handled {
				if terminal {
					return
				}
				continue
			}
		}

		result := s.engine.Execute(command)
		if err := s.writeResponse(connection, bufferedWriter, writer, result.Response); err != nil {
			return
		}
		if result.Close {
			return
		}
	}
}

func (s *Server) writeResponse(
	connection net.Conn,
	bufferedWriter *bufio.Writer,
	writer *resp.Writer,
	response resp.Value,
) error {
	if s.config.WriteTimeout > 0 {
		_ = connection.SetWriteDeadline(time.Now().Add(s.config.WriteTimeout))
	}
	if err := writer.WriteValue(response); err != nil {
		return err
	}
	return bufferedWriter.Flush()
}

func (s *Server) trackConnection(connection net.Conn) bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.closing {
		return false
	}
	s.connections[connection] = struct{}{}
	s.waitGroup.Add(1)
	return true
}

func (s *Server) untrackConnection(connection net.Conn) {
	s.mutex.Lock()
	delete(s.connections, connection)
	s.mutex.Unlock()
	s.waitGroup.Done()
}

func (s *Server) isClosing() bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.closing
}
