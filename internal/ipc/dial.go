package ipc

import (
	"errors"
	"os"
	"strconv"
	"time"
)

// Environment variables the runtime publishes so a handler process knows where
// to connect.
const (
	socketEnvVar          = "CELERITY_RUNTIME_SOCKET"
	fallbackEnabledEnvVar = "CELERITY_RUNTIME_SOCKET_FALLBACK_ENABLED"
	fallbackPortEnvVar    = "CELERITY_RUNTIME_SOCKET_FALLBACK_PORT"
)

// DialConfig says where the runtime is serving the handler stream.
type DialConfig struct {
	// Socket is the Unix socket path, which is the normal case.
	Socket string
	// FallbackEnabled reports that the runtime could not bind a socket and is
	// serving over loopback TCP instead.
	//
	// The SDK uses the fallback only when the runtime says it did. Falling back
	// on its own would widen who may register as a handler from the one user the
	// socket's permissions allow to any process that can reach loopback.
	FallbackEnabled bool
	FallbackPort    int

	// Retry bounds how long dialling is retried. A handler process is commonly
	// started alongside the runtime, so losing the race to its listener is
	// ordinary rather than fatal.
	RetryFor time.Duration
}

// DialConfigFromEnv reads the dial configuration the runtime publishes.
func DialConfigFromEnv() DialConfig {
	port, _ := strconv.Atoi(os.Getenv(fallbackPortEnvVar))
	enabled, _ := strconv.ParseBool(os.Getenv(fallbackEnabledEnvVar))

	return DialConfig{
		Socket:          os.Getenv(socketEnvVar),
		FallbackEnabled: enabled,
		FallbackPort:    port,
		RetryFor:        30 * time.Second,
	}
}

// Target returns the gRPC target to dial, preferring the Unix socket.
func (c DialConfig) Target() (string, error) {
	if c.Socket != "" {
		return "unix://" + c.Socket, nil
	}
	if c.FallbackEnabled && c.FallbackPort > 0 {
		return "127.0.0.1:" + strconv.Itoa(c.FallbackPort), nil
	}
	return "", errors.New(
		"the runtime published no handler stream address: expected " + socketEnvVar +
			" to name a unix socket, or " + fallbackEnabledEnvVar + " with " + fallbackPortEnvVar,
	)
}
