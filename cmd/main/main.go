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
	"github.com/FA25SE050-RogueLearn/RogueLearn.Executor/pkg/env"
	"github.com/FA25SE050-RogueLearn/RogueLearn.Executor/protos"
	"github.com/lmittmann/tint"
	"google.golang.org/grpc"
)

func main() {
	// Logger setup
	slogHandler := tint.NewHandler(os.Stdout, &tint.Options{Level: slog.LevelDebug, AddSource: true})
	logger := slog.New(slogHandler)
	slog.SetDefault(logger)

	httpPort := env.GetInt("EXECUTOR_HTTP_PORT", 8082)
	grpcPort := env.GetInt("EXECUTOR_GRPC_PORT", 8083)

	cfg := &api.Config{
		HttpPort: httpPort,
		GrpcPort: grpcPort,
	}

	maxWorkers := env.GetInt("EXECUTOR_MAX_WORKERS", 3)
	memoryLimitMB := env.GetInt("EXECUTOR_MEMORY_LIMIT_MB", 256)
	maxJobs := env.GetInt("EXECUTOR_MAX_JOBS", 50)
	cpuNanoLimit := env.GetInt("EXECUTOR_CPU_NANO_LIMIT", 1_000_000_000)

	// Initialize worker pool
	workerPoolOpts := &executor.WorkerPoolOptions{
		MaxWorkers:    maxWorkers,
		MemoryLimitMB: int64(memoryLimitMB),
		MaxJobCount:   maxJobs,
		CpuNanoLimit:  int64(cpuNanoLimit),
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
