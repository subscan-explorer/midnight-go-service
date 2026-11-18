package main

import (
	"context"
	"errors"
	"log"
	"midnight-go-sdk/config"
	"midnight-go-sdk/logger"
	"midnight-go-sdk/network"
	"midnight-go-sdk/utils"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"midnight-go-sdk/compile"
)

func main() {
	// logger initialization
	logger.NewLogger()

	// download compiler versions if "download" argument is provided
	if args := os.Args; len(args) > 1 && args[1] == "download" {
		if err := Download(); err != nil {
			log.Fatalf("Failed to download compiler versions: %v", err)
		}
		return
	}

	// STATIC_DIR is the directory where compiler compactC and verified contracts are stored.
	staticDir := utils.GetEnv("STATIC_DIR", "static")
	// TEMP_DIR is the directory for temporary files during compilation.
	tempDir := utils.GetEnv("TEMP_DIR", "tmp")
	conf, err := config.NewNetworkConfig()
	if err != nil {
		log.Fatalf("Invalid network configuration: %v", err)
	}

	// Create a temporary directory for compilation if it doesn't exist.
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		log.Fatalf("Failed to create temp directory: %v", err)
	}
	runtimeLimits := compile.NewRuntime()
	compilerConfig := &compile.Config{
		StaticDir:   staticDir,
		TempDir:     tempDir,
		Client:      network.NewClient(conf.GetNode()),
		NetworkConf: conf,
		Runtime:     runtimeLimits,
	}

	if conf.GetNetworkID() == config.Stagenet {
		if _, err := resolveCompileByNetwork(context.Background(), compilerConfig, ""); err != nil {
			log.Fatalf("Invalid Stagenet isolation: %v", err)
		}
	}
	http.HandleFunc("/verify", compileHandler(compilerConfig))
	// get contract chain state
	http.HandleFunc("/contract/state", stateHandler(compilerConfig))
	http.HandleFunc("/contract/explorer", explorerHandler(compilerConfig))

	port := utils.GetEnv("PORT", "8080")
	srv := &http.Server{
		Addr:              ":" + port,
		ReadHeaderTimeout: runtimeLimits.HTTPReadHeaderTimeout,
		ReadTimeout:       runtimeLimits.HTTPReadTimeout,
		WriteTimeout:      runtimeLimits.HTTPWriteTimeout,
		IdleTimeout:       runtimeLimits.HTTPIdleTimeout,
	}
	// Socket write deadlines do not cancel handlers or their queue waits.
	srv.Handler = requestDeadline(requestLogging(http.DefaultServeMux), runtimeLimits.HTTPWriteTimeout)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	listener, err := net.Listen("tcp", srv.Addr)
	if err == nil {
		logger.Event(ctx, "INFO", "server_started", logger.Fields{"port": port, "network": string(conf.GetNetworkID()), "limits": runtimeLimits.String()})
		err = serveUntilShutdown(ctx, srv, listener, runtimeLimits.Close, 15*time.Second)
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("Failed to start server: %v", err)
	}
}

func requestDeadline(next http.Handler, writeTimeout time.Duration) http.Handler {
	// Reserve time to send the cancellation response before the socket expires.
	timeout := writeTimeout - minDuration(time.Second, writeTimeout/10)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func serveUntilShutdown(ctx context.Context, srv *http.Server, listener net.Listener, closeRuntime func(), grace time.Duration) error {
	requests, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()
	defer closeRuntime()
	srv.BaseContext = func(net.Listener) context.Context { return requests }
	served := make(chan error, 1)
	go func() { served <- srv.Serve(listener) }()
	select {
	case err := <-served:
		return err
	case <-ctx.Done():
	}
	logger.Event(context.Background(), "INFO", "server_stopping", logger.Fields{"grace_ms": grace.Milliseconds()})
	shutdownCtx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	err := srv.Shutdown(shutdownCtx)
	if err != nil {
		logger.Event(context.Background(), "WARN", "server_shutdown_timeout", logger.Fields{"error_code": logger.ErrorCode(err)})
		cancelRequests()
		_ = srv.Close()
	}
	serveErr := <-served
	logger.Event(context.Background(), "INFO", "server_stopped", logger.Fields{"drained": err == nil})
	if err != nil {
		return err
	}
	return serveErr
}
