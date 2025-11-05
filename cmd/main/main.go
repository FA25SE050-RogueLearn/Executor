package main

import (
	"fmt"
	"log"
	"log/slog"
	"net"
	"os"
	"runtime/debug"

	"github.com/FA25SE050-RogueLearn/RogueLearn.Executor/cmd/api"
	"github.com/FA25SE050-RogueLearn/RogueLearn.Executor/internal/executor"
	httpHandlers "github.com/FA25SE050-RogueLearn/RogueLearn.Executor/internal/handlers/http"
	protoHandlers "github.com/FA25SE050-RogueLearn/RogueLearn.Executor/internal/handlers/proto"
	"github.com/FA25SE050-RogueLearn/RogueLearn.Executor/protos"
	"github.com/lmittmann/tint"
	"google.golang.org/grpc"
)

func main() {
	// Logger setup
	slogHandler := tint.NewHandler(os.Stdout, &tint.Options{Level: slog.LevelDebug, AddSource: true})
	logger := slog.New(slogHandler)
	slog.SetDefault(logger)

	cfg := &api.Config{
		HttpPort: 8081,
		GrpcPort: 8082,
	}

	// Initialize worker pool
	// With 2GB RAM and 2 CPU cores per replica:
	// - Each container gets 256MB (allows 8 concurrent containers per replica)
	// - Each container gets 0.5 CPU cores
	// - MaxWorkers set to 10 (can handle 10 concurrent jobs per replica)
	// - MaxJobCount set to 50 (reasonable queue size)
	workerPoolOpts := &executor.WorkerPoolOptions{
		MaxWorkers:       10,                // Number of worker goroutines
		MemoryLimitBytes: 256 * 1024 * 1024, // 256 MB per container
		MaxJobCount:      50,                // Maximum number of queued jobs
		CpuNanoLimit:     500_000_000,       // 0.5 CPU core per container (500 million nanoseconds)
	}

	workerPool, err := executor.NewWorkerPool(logger, workerPoolOpts)
	if err != nil {
		log.Fatalf("failed to initialize worker pool: %v", err)
	}

	// Ensure worker pool shuts down gracefully
	defer workerPool.Shutdown()

	// Initialize the executor
	engine := executor.NewExecutor(logger, workerPool)

	// Initialize HTTP Handler
	handler := httpHandlers.NewHandler(logger, engine)

	app := api.NewApplication(cfg, logger, handler)

	// Start gRPC server
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.GrpcPort))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	var opts []grpc.ServerOption
	grpcServer := grpc.NewServer(opts...)
	executorServer := protoHandlers.NewExecutorGRPCServer(engine, logger)
	protos.RegisterExecutorServiceServer(grpcServer, executorServer)

	app.Logger.Info("starting gRPC executor service", "port", cfg.GrpcPort)
	go grpcServer.Serve(lis)

	// run HTTP server
	err = app.Run()
	if err != nil {
		// Using standard log here to be absolutely sure it prints if slog itself had an issue
		log.Printf("CRITICAL ERROR from run(): %v\n", err)
		currentTrace := string(debug.Stack())
		log.Printf("Trace: %s\n", currentTrace)
		// Also log with slog if it's available
		slog.Error("CRITICAL ERROR from run()", "error", err.Error(), "trace", currentTrace)
		os.Exit(1)
	}
}
