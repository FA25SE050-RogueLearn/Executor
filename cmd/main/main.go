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
		HttpPort: 8082,
		GrpcPort: 8083,
	}

	// Initialize worker pool
	// Optimized for i7-8700 (6 cores / 12 threads):
	// - MaxWorkers: 6 (matches physical cores for optimal cache locality)
	// - Each container gets 512MB RAM (3GB total for 6 containers)
	// - Each container gets 2.0 CPU cores (12 total, using hyperthreading)
	// - MaxJobCount: 50 (reasonable queue size)
	// This prevents CPU overcommitment and reduces throttling
	workerPoolOpts := &executor.WorkerPoolOptions{
		MaxWorkers:       5,
		MemoryLimitBytes: 256,           // 512MB per container
		MaxJobCount:      50,            // Maximum number of queued jobs
		CpuNanoLimit:     1_500_000_000, // 1.0 cores per container
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
