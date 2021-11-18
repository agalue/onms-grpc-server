package main

import (
	"github.com/agalue/onms-grpc-server/protobuf/twin"
	"go.uber.org/zap"
)

// OnmsGrpcTwin represents an OpenNMS Twin gRPC Server instance.
type OnmsGrpcTwin struct {
	twin.UnimplementedOpenNMSTwinIpcServer

	config *ServerConfig
	log    *zap.SugaredLogger
}

// Start initializes the OpenNMS Twin Server
func (srv *OnmsGrpcTwin) Start(config *ServerConfig, logger *zap.SugaredLogger) error {
	srv.config = config
	srv.log = logger
	return nil
}

// Stop shutsdown the OpenNMS Twin Server
func (srv *OnmsGrpcTwin) Stop() {
}

// SinkStreaming streams Twin updates from OpenNMS to Minion (server-side streaming gRPC).
func (srv *OnmsGrpcTwin) SinkStreaming(header *twin.MinionHeader, stream twin.OpenNMSTwinIpc_SinkStreamingServer) error {
	srv.log.Warnf("Sink for Twin API not implemented (coming soon)")
	return nil
}

// RpcStreaming streams Twin request/response between OpenNMS and Minion (bidirectional streaming gRPC).
func (srv *OnmsGrpcTwin) RpcStreaming(stream twin.OpenNMSTwinIpc_RpcStreamingServer) error {
	srv.log.Warnf("RPC for Twin API not implemented (coming soon)")
	return nil
}
