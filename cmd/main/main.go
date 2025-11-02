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
	"github.com/FA25SE050-RogueLearn/RogueLearn.Executor/internal/k8s"
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

	// Initialize code builder
	pkgAnalyzer := executor.NewGoPackageAnalyzer()
	codeBuilder := executor.NewCodeBuilder([]executor.PackageAnalyzer{pkgAnalyzer}, logger)

	// Initialize K8s Client
	k8sCli, err := k8s.GetK8sClient()
	if err != nil {
		log.Fatalf("failed to init k8s client: %v", err)
	}

	// Initialize the executor
	engine := executor.NewExecutorEngine(logger, k8sCli, codeBuilder)

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
