package main

import (
	//	"io"

	"github.com/agalue/onms-grpc-server/protobuf/twin"
	"go.uber.org/zap"
	//	"google.golang.org/grpc/codes"
	//	"google.golang.org/grpc/status"
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
	srv.log.Warnf("Twin API not implemented, ignoring") // FIXME
	return nil
}

// Stop shutsdown the OpenNMS Twin Server
func (srv *OnmsGrpcTwin) Stop() {
}

// SinkStreaming streams Twin updates from OpenNMS to Minion (server-side streaming gRPC).
func (srv *OnmsGrpcTwin) SinkStreaming(header *twin.MinionHeader, stream twin.OpenNMSTwinIpc_SinkStreamingServer) error {
	/*
		srv.log.Debugf("received Twin Sink message from %s at location %s", header.SystemId, header.Location)
		srv.log.Warnf("Twin API for Sink not implemented, ignoring") // FIXME
	*/
	return nil
}

// RpcStreaming streams Twin request/response between OpenNMS and Minion (bidirectional streaming gRPC).
func (srv *OnmsGrpcTwin) RpcStreaming(stream twin.OpenNMSTwinIpc_RpcStreamingServer) error {
	/*
		srv.log.Infof("starting Twin RPC API stream")
		for {
			msg, err := stream.Recv()
			if err == io.EOF {
				break
			}
			if err != nil {
				if errStatus, ok := status.FromError(err); ok {
					return status.Errorf(errStatus.Code(), "cannot receive Twin RPC message: %v ", errStatus.Message())
				}
				return status.Errorf(codes.Unknown, "unknown problem with Twin RPC message: %v", err)
			}
			srv.log.Debugf("received Twin RPC message from %s at location %s with consumer key %s", msg.SystemId, msg.Location, msg.ConsumerKey)
			srv.log.Warnf("Twin API for RPC not implemented, ignoring") // FIXME
		}
		srv.log.Warnf("terminating RPC API stream")
	*/
	return nil
}
