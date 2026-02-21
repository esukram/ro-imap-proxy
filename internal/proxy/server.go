package proxy

import (
	"errors"
	"log/slog"
	"net"
	"sync"

	"ro-imap-proxy/internal/config"
)

// Server listens for incoming client connections and spawns sessions.
type Server struct {
	config   *config.Config
	mu       sync.Mutex
	listener net.Listener
	logger   *slog.Logger
}

// NewServer creates a new Server with the given config and logger.
func NewServer(cfg *config.Config, logger *slog.Logger) *Server {
	return &Server{
		config: cfg,
		logger: logger,
	}
}

// ListenAndServe binds a TCP listener on cfg.Server.Listen and starts accepting connections.
func (s *Server) ListenAndServe() error {
	l, err := net.Listen("tcp", s.config.Server.Listen)
	if err != nil {
		return err
	}
	s.listener = l
	return s.Serve(l)
}

// Serve accepts connections on the provided listener, spawning a session goroutine per connection.
func (s *Server) Serve(l net.Listener) error {
	s.mu.Lock()
	s.listener = l
	s.mu.Unlock()
	for {
		conn, err := l.Accept()
		if err != nil {
			// A closed listener returns an error; treat that as clean shutdown.
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		s.logger.Info("new connection", "client", conn.RemoteAddr())
		sess := NewSession(conn, s.config, s.logger)
		go sess.Run()
	}
}

// Close shuts down the listener, causing Serve/ListenAndServe to return.
func (s *Server) Close() error {
	s.mu.Lock()
	l := s.listener
	s.mu.Unlock()
	if l != nil {
		return l.Close()
	}
	return nil
}
