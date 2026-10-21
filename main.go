package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"k8s-deploy-api/internal/httpapi"
	"k8s-deploy-api/internal/kube"
)

const (
	listenAddress  = "127.0.0.1:8080"
	startupTimeout = 10 * time.Second
)

func main() {
	kubeconfig := flag.String("kubeconfig", "", "kubeconfig path (default: client-go loading rules)")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *kubeconfig, serve); err != nil {
		log.Printf("startup or serving failed: %v", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, path string, startServer func(context.Context) error) error {
	client, err := kube.Load(path)
	if err != nil {
		return err
	}
	checkCtx, cancel := context.WithTimeout(ctx, startupTimeout)
	info, err := kube.Check(checkCtx, client)
	cancel()
	if err != nil {
		return fmt.Errorf("startup check for context %q and namespace %q: %w", kube.ContextName, kube.Namespace, err)
	}
	log.Printf("Kubernetes startup check passed context=%s namespace=%s version=%s", kube.ContextName, kube.Namespace, info.GitVersion)
	return startServer(ctx)
}

func serve(ctx context.Context) error {
	server := &http.Server{
		Addr:              listenAddress,
		Handler:           httpapi.NewHandler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	result := make(chan error, 1)
	go func() { result <- server.ListenAndServe() }()
	log.Printf("HTTP server starting address=%s", listenAddress)
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return fmt.Errorf("shut down HTTP server: %w", err)
		}
		if err := <-result; !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}
