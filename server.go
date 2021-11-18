package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"

	"github.com/agalue/onms-grpc-server/protobuf/ipc"
	"github.com/agalue/onms-grpc-server/protobuf/twin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"

	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	grpc_zap "github.com/grpc-ecosystem/go-grpc-middleware/logging/zap"
	grpc_prometheus "github.com/grpc-ecosystem/go-grpc-prometheus"
)

// OnmsGrpcServer represents an OpenNMS gRPC Server instance that implements the IPC API and Twin API
// It requires Kafka configured with single-topic enabled for RPC API requests.
type OnmsGrpcServer struct {
	Config *ServerConfig
	Logger *zap.Logger

	server   *grpc.Server
	health   *health.Server
	onmsIPC  *OnmsGrpcIPC
	onmsTwin *OnmsGrpcTwin

	log *zap.SugaredLogger
}

// Start initiates the gRPC server and the Kafka Producer instances.
func (srv *OnmsGrpcServer) Start() error {
	var err error

	if srv.server != nil {
		return nil // Silently ignore if producer was already initialized
	}

	if srv.Logger == nil {
		return fmt.Errorf("zap logger not initialized")
	}
	srv.log = srv.Logger.Sugar()

	jsonBytes, _ := json.MarshalIndent(srv, "", "  ")
	srv.log.Infof("initializing gRPC server: %s", string(jsonBytes))

	srv.onmsIPC = new(OnmsGrpcIPC)
	if err = srv.onmsIPC.Start(srv.Config, srv.Logger.Sugar()); err != nil {
		return err
	}

	srv.onmsTwin = new(OnmsGrpcTwin)
	if err = srv.onmsTwin.Start(srv.Config, srv.Logger.Sugar()); err != nil {
		return err
	}

	if err = srv.initGrpcServer(); err != nil {
		return err
	}
	srv.initPrometheus()
	return nil
}

// Stop gracefully stops the gRPC server and the Kafka Producer/Consumer instances.
func (srv *OnmsGrpcServer) Stop() {
	srv.log.Warnf("shutting down...")
	if srv.server != nil {
		srv.server.Stop()
	}
	if srv.health != nil {
		srv.health.Shutdown()
	}
	if srv.onmsIPC != nil {
		srv.onmsIPC.Stop()
	}
	if srv.onmsTwin != nil {
		srv.onmsTwin.Stop()
	}
	srv.log.Infof("done!")
}

// Initializes the gRPC Server
func (srv *OnmsGrpcServer) initGrpcServer() error {
	if srv.onmsIPC == nil {
		return fmt.Errorf("OpenNMS IPC gRPC Server Instance not initialized")
	}

	// Initialize gRPC Server
	options := make([]grpc.ServerOption, 0)
	if srv.Config.TLSEnabled {
		if option, err := srv.createTLSConfig(); err == nil {
			options = append(options, option)
		} else {
			return err
		}
	}
	options = append(options, grpc_middleware.WithStreamServerChain(
		grpc_zap.StreamServerInterceptor(srv.Logger),
		grpc_prometheus.StreamServerInterceptor,
	))
	srv.server = grpc.NewServer(options...)
	srv.health = health.NewServer()

	// Configure gRPC Services
	ipc.RegisterOpenNMSIpcServer(srv.server, srv.onmsIPC)
	twin.RegisterOpenNMSTwinIpcServer(srv.server, srv.onmsTwin)
	grpc_health_v1.RegisterHealthServer(srv.server, srv.health)
	grpc_prometheus.Register(srv.server)

	// Display configured gRPC Services
	jsonBytes, _ := json.MarshalIndent(srv.server.GetServiceInfo(), "", "  ")
	srv.log.Infof("gRPC server info: %s", string(jsonBytes))

	// Initialize TCP Listener
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", srv.Config.GrpcPort))
	if err != nil {
		return fmt.Errorf("cannot initialize listener: %v", err)
	}

	// Start gRPC Server
	go func() {
		srv.log.Infof("starting gRPC server on port %d", srv.Config.GrpcPort)
		if err = srv.server.Serve(listener); err != nil {
			srv.log.Fatalf("could not serve: %v", err)
		}
	}()

	return nil
}

// Creates the TLS configuration
func (srv *OnmsGrpcServer) createTLSConfig() (grpc.ServerOption, error) {
	cert, err := tls.LoadX509KeyPair(srv.Config.TLSServerCertFile, srv.Config.TLSServerKeyFile)
	if err != nil {
		return nil, err
	}
	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
	}
	if srv.Config.TLSClientCACertFile != "" {
		certPool := x509.NewCertPool()
		bs, err := ioutil.ReadFile(srv.Config.TLSClientCACertFile)
		if err != nil {
			return nil, err
		}
		if ok := certPool.AppendCertsFromPEM(bs); !ok {
			return nil, fmt.Errorf("failed to append client certs")
		}
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
		cfg.ClientCAs = certPool
	}
	creds := credentials.NewTLS(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to setup TLS: %v", err)
	}
	return grpc.Creds(creds), nil
}

// Registers the prometheus metrics and starts the HTTP server
func (srv *OnmsGrpcServer) initPrometheus() {
	if srv.onmsIPC == nil {
		return
	}
	prometheus.MustRegister(srv.onmsIPC.GetCollectors()...)
	go func() {
		srv.log.Infof("starting Prometheus Metrics server %d", srv.Config.HTTPPort)
		http.Handle("/metrics", promhttp.Handler())
		http.ListenAndServe(fmt.Sprintf(":%d", srv.Config.HTTPPort), nil)
	}()
}
