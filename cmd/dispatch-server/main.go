package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/liliang-cn/dispatch/pkg/server"
	pb "github.com/liliang-cn/dispatch/proto"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

var (
	port      int
	configPath string
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "dispatch-server",
		Short: "dispatch gRPC server",
		RunE:  runServer,
	}

	rootCmd.Flags().IntVarP(&port, "port", "p", 50051, "gRPC server port")
	rootCmd.Flags().StringVarP(&configPath, "config", "c", "", "Config file path")

	if err := rootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}

func runServer(cmd *cobra.Command, args []string) error {
	// 创建服务
	srv, err := server.NewServer(configPath)
	if err != nil {
		return fmt.Errorf("failed to create server: %w", err)
	}

	// 创建 gRPC 监听器
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}

	// 创建 gRPC 服务器
	grpcServer := grpc.NewServer(
		grpc.MaxRecvMsgSize(100*1024*1024), // 100MB
		grpc.MaxSendMsgSize(100*1024*1024),
	)

	// 注册服务
	pb.RegisterDispatchServer(grpcServer, srv)

	// 注册反射服务
	reflection.Register(grpcServer)

	// 启动服务器
	go func() {
		log.Printf("dispatch-server listening on :%d\n", port)
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("failed to serve: %v", err)
		}
	}()

	// 等待中断信号
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-sigCh:
		log.Println("Shutting down...")
	case <-ctx.Done():
	}

	// 优雅关闭
	stopped := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(stopped)
	}()

	select {
	case <-stopped:
		log.Println("Server stopped")
	case <-time.After(10 * time.Second):
		log.Println("Timeout, forcing stop")
		grpcServer.Stop()
	}

	return nil
}
