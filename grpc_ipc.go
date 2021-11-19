package main

import (
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/agalue/onms-grpc-server/protobuf/ipc"
	"github.com/agalue/onms-grpc-server/protobuf/rpc"
	"github.com/agalue/onms-grpc-server/protobuf/sink"
	"github.com/confluentinc/confluent-kafka-go/kafka"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// OnmsGrpcIPC represents an OpenNMS IPC gRPC Server instance.
// It requires Kafka configured with single-topic for RPC API requests.
type OnmsGrpcIPC struct {
	ipc.UnimplementedOpenNMSIpcServer

	config *ServerConfig

	producer  KafkaProducer
	consumers map[string]KafkaConsumer
	log       *zap.SugaredLogger

	rpcHandlerByLocation sync.Map // key: location, value: RoundRobinHandlerMap of RPC handlers by ID
	rpcHandlerByMinionID sync.Map // key: minion ID, value: RPC handler
	currentChunkCache    sync.Map // key: RPC message ID, value: current chunk number
	messageCache         sync.Map // key: RPC message ID, value: byte slice
	rpcDelayQueue        sync.Map // key: RPC message ID, value: RPC expiration time

	metricDeliveredErrors   *prometheus.CounterVec
	metricDeliveredBytes    *prometheus.CounterVec
	metricDeliveredMessages *prometheus.CounterVec
	metricReceivedErrors    *prometheus.CounterVec
	metricReceivedMessages  *prometheus.CounterVec
	metricReceivedBytes     *prometheus.CounterVec
	metricExpiredMessages   prometheus.Counter
}

// Main Public Methods

// Start initializes the OpenNMS IPC Server
func (srv *OnmsGrpcIPC) Start(config *ServerConfig, logger *zap.SugaredLogger) error {
	srv.initVariables(config, logger)
	srv.initDelayQueueProcessor()
	return srv.initKafkaProducer()
}

// Stop shutsdown the OpenNMS IPC Server
func (srv *OnmsGrpcIPC) Stop() {
	srv.producer.Close()
	for _, consumer := range srv.consumers {
		consumer.Unsubscribe()
		consumer.Close()
	}
}

// GetCollectors returns a list of prometheus metrics
func (srv *OnmsGrpcIPC) GetCollectors() []prometheus.Collector {
	return []prometheus.Collector{
		srv.metricDeliveredErrors,
		srv.metricDeliveredBytes,
		srv.metricDeliveredMessages,
		srv.metricReceivedErrors,
		srv.metricReceivedBytes,
		srv.metricReceivedMessages,
		srv.metricExpiredMessages,
	}
}

// Main gRPC Methods

// SinkStreaming streams Sink API messages from Minion to OpenNMS (client-side streaming gRPC).
func (srv *OnmsGrpcIPC) SinkStreaming(stream ipc.OpenNMSIpc_SinkStreamingServer) error {
	srv.log.Infof("starting Sink API stream")
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			if errStatus, ok := status.FromError(err); ok {
				return status.Errorf(errStatus.Code(), "error while receiving Sink API data: %v ", errStatus.Message())
			}
			return status.Errorf(codes.Unknown, "error while receiving Sink API data: %v", err)
		}
		srv.transformAndSendSinkMessage(msg)
	}
	srv.log.Warnf("terminating Sink API stream")
	return nil
}

// RpcStreaming streams RPC API messages between OpenNMS and Minion (bidirectional streaming gRPC).
func (srv *OnmsGrpcIPC) RpcStreaming(stream ipc.OpenNMSIpc_RpcStreamingServer) error {
	srv.log.Infof("starting RPC API stream")
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			if errStatus, ok := status.FromError(err); ok {
				return status.Errorf(errStatus.Code(), "cannot receive RPC API response: %v ", errStatus.Message())
			}
			return status.Errorf(codes.Unknown, "unknown problem with RPC API response: %v", err)
		}
		if srv.isHeaders(msg) {
			srv.addRPCHandler(msg.Location, msg.SystemId, stream)
			if err := srv.startConsumingForLocation(msg.Location); err != nil {
				srv.log.Errorf("cannot start consuming from location %s: %v", msg.Location, err)
			}
		} else {
			srv.transformAndSendRPCMessage(msg)
		}
	}
	srv.log.Warnf("terminating RPC API stream")
	return nil
}

// Initialization Methods

// Initializes all the internal variables
func (srv *OnmsGrpcIPC) initVariables(config *ServerConfig, log *zap.SugaredLogger) {
	srv.consumers = make(map[string]KafkaConsumer)
	srv.rpcHandlerByLocation = sync.Map{}
	srv.rpcHandlerByMinionID = sync.Map{}
	srv.currentChunkCache = sync.Map{}
	srv.messageCache = sync.Map{}
	srv.rpcDelayQueue = sync.Map{}

	srv.metricDeliveredErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "onms_kafka_producer_delivered_errors",
		Help: "The total number of message delivery errors per topic associated with the Kafka producer",
	}, []string{"topic"})
	srv.metricDeliveredBytes = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "onms_kafka_producer_delivered_bytes",
		Help: "The total number of bytes delivered per topic associated with the Kafka producer",
	}, []string{"topic"})
	srv.metricDeliveredMessages = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "onms_kafka_producer_delivered_messages",
		Help: "The number of messages delivered per topic associated with the Kafka producer",
	}, []string{"topic"})
	srv.metricReceivedErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "onms_kafka_consumer_received_errors",
		Help: "The total number of errors received per location associated with a Kafka consumer",
	}, []string{"location"})
	srv.metricReceivedBytes = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "onms_kafka_consumer_received_bytes",
		Help: "The total number of messages received per location associated with a Kafka consumer",
	}, []string{"location"})
	srv.metricReceivedMessages = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "onms_kafka_consumer_received_messages",
		Help: "The total number of bytes received per location associated with a Kafka consumer",
	}, []string{"location"})
	srv.metricExpiredMessages = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "onms_kafka_expired_messages",
		Help: "The total number of expired messages per location due to TTL",
	})

	srv.config = config
	srv.log = log
}

// Initializes the Kafka Producer
func (srv *OnmsGrpcIPC) initKafkaProducer() error {
	var err error

	// Silently ignore if producer was already initialized
	if srv.producer != nil {
		return nil
	}

	// Initialize Producer Configuration
	config := &kafka.ConfigMap{"bootstrap.servers": srv.config.KafkaBootstrap}
	if err := srv.config.UpdateKafkaConfig(config, srv.config.KafkaProducerProperties); err != nil {
		return err
	}

	// Initialize Kafka Producer
	if srv.producer, err = kafka.NewProducer(config); err != nil {
		return fmt.Errorf("cannot create kafka producer: %v", err)
	}

	// Initialize Producer Message Logger
	go func() {
		for e := range srv.producer.Events() {
			switch ev := e.(type) {
			case *kafka.Message:
				if ev.TopicPartition.Error != nil {
					srv.metricDeliveredErrors.WithLabelValues(*ev.TopicPartition.Topic).Inc()
					srv.log.Errorf("kafka delivery failed: %v", ev.TopicPartition)
				} else {
					bytes := len(ev.Value)
					srv.metricDeliveredMessages.WithLabelValues(*ev.TopicPartition.Topic).Inc()
					srv.metricDeliveredBytes.WithLabelValues(*ev.TopicPartition.Topic).Add(float64(bytes))
					srv.log.Debugf("kafka delivered message of %d bytes with key %s to %v", bytes, ev.Key, ev.TopicPartition)
				}
			default:
				srv.log.Debugf("kafka event: %s", ev)
			}
		}
	}()

	return nil
}

// Initializes a goroutine to cleanup expired RPC messages from the caches.
func (srv *OnmsGrpcIPC) initDelayQueueProcessor() {
	go func() {
		for now := range time.Tick(time.Second) {
			srv.rpcDelayQueue.Range(func(key interface{}, value interface{}) bool {
				rpcID := key.(string)
				expiration := value.(uint64)
				if uint64(now.Unix()) > expiration {
					srv.log.Warnf("RPC message %s expired", rpcID)
					srv.metricExpiredMessages.Inc()
					srv.rpcDelayQueue.Delete(rpcID)
					srv.messageCache.Delete(rpcID)
					srv.currentChunkCache.Delete(rpcID)
				}
				return true
			})
		}
	}()
}

// Sink API Methods

// Transforms IPC SinkMessage into a set of Sink API SinkMessages and send them to Kafka.
func (srv *OnmsGrpcIPC) transformAndSendSinkMessage(msg *ipc.SinkMessage) {
	totalChunks := srv.getTotalChunks(msg.Content)
	for chunk := int32(0); chunk < totalChunks; chunk++ {
		bufferSize := srv.getRemainingBufferSize(int32(len(msg.Content)), chunk)
		offset := chunk * srv.getMaxBufferSize()
		data := msg.Content[offset : offset+bufferSize]
		sinkMsg := &sink.SinkMessage{
			MessageId:          msg.MessageId,
			CurrentChunkNumber: chunk,
			TotalChunks:        totalChunks,
			Content:            data,
		}
		if bytes, err := proto.Marshal(sinkMsg); err != nil {
			srv.log.Errorf("cannot serialize sink message: %v", err)
		} else {
			srv.log.Debugf("sending %s message %s via Sink API for location %s", msg.ModuleId, msg.MessageId, msg.Location)
			srv.sendToKafka(srv.getSinkTopic(msg.ModuleId), msg.MessageId, bytes)
		}
	}
}

// Builds the Sink API topic for agiven module.
func (srv *OnmsGrpcIPC) getSinkTopic(module string) string {
	return fmt.Sprintf(sinkTopicNameWithoutLocation, srv.config.OnmsInstanceID, sinkModulePrefix, module)
}

// RPC API Methods

// Checks whether or not the RPC API Response is a header message.
func (srv *OnmsGrpcIPC) isHeaders(msg *ipc.RpcResponseProto) bool {
	return msg.SystemId != "" && msg.RpcId == msg.SystemId
}

// Registers a new RPC API handler for a given location and system ID (or Minion ID).
// It replaces the existing one if there is any.
func (srv *OnmsGrpcIPC) addRPCHandler(location string, systemID string, rpcHandler ipc.OpenNMSIpc_RpcStreamingServer) {
	if location == "" || systemID == "" {
		srv.log.Errorf("invalid metadata received with location = '%s', systemId = '%s'", location, systemID)
		return
	}
	obj, _ := srv.rpcHandlerByLocation.LoadOrStore(location, &RoundRobinHandlerMap{})
	handlerMap := obj.(*RoundRobinHandlerMap)
	action := "added"
	if handlerMap.Contains(systemID) {
		action = "replaced"
	}
	srv.log.Infof("%s RPC handler for minion %s at location %s", action, systemID, location)
	handlerMap.Set(systemID, rpcHandler)
	srv.rpcHandlerByMinionID.Store(systemID, rpcHandler)
}

// Initializes a new goroutine with a Kafka consumer for a given location.
// Should be called once after registering the RPC API handler for the same location.
// Consumer is not initialized if there is already one.
func (srv *OnmsGrpcIPC) startConsumingForLocation(location string) error {
	if srv.consumers[location] != nil {
		return nil
	}

	kafkaConfig := &kafka.ConfigMap{
		"bootstrap.servers":       srv.config.KafkaBootstrap,
		"group.id":                fmt.Sprintf("%s.%s", srv.config.OnmsInstanceID, location),
		"auto.commit.interval.ms": 1000,
	}
	if err := srv.config.UpdateKafkaConfig(kafkaConfig, srv.config.KafkaConsumerProperties); err != nil {
		return err
	}

	consumer, err := kafka.NewConsumer(kafkaConfig)
	if err != nil {
		return fmt.Errorf("could not create producer: %v", err)
	}

	topic := srv.getRequestTopicAtLocation(location)
	if err := consumer.Subscribe(topic, nil); err != nil {
		return fmt.Errorf("cannot subscribe to topic %s: %v", topic, err)
	}
	srv.log.Infof("subscribed to topic %s", topic)

	go func() {
		srv.log.Infof("starting RPC consumer for location %s", location)
		for {
			event := consumer.Poll(100)
			switch e := event.(type) {
			case *kafka.Message:
				rpcMsg := &rpc.RpcMessageProto{}
				if err := proto.Unmarshal(e.Value, rpcMsg); err != nil {
					srv.log.Warnf("invalid message received: %v", err)
					continue
				}
				request := srv.createRPCRequest(location, rpcMsg)
				if request != nil {
					srv.sendRequest(location, request)
				}
			case kafka.Error:
				srv.log.Errorf("kafka consumer error %v", e)
				srv.metricReceivedErrors.WithLabelValues(location).Inc()
			}
		}
	}()

	srv.consumers[location] = consumer
	return nil
}

// Transforms RPC API Message into a set of IPC RPC Messages after all the chunks were processed.
// Returns nil if the message is incomplete.
func (srv *OnmsGrpcIPC) createRPCRequest(location string, rpcMsg *rpc.RpcMessageProto) *ipc.RpcRequestProto {
	rpcContent := rpcMsg.RpcContent
	srv.metricReceivedMessages.WithLabelValues(location).Inc()
	srv.metricReceivedBytes.WithLabelValues(location).Add(float64(len(rpcContent)))
	srv.log.Debugf("processing %s RPC message %s %d/%d at location %s", rpcMsg.ModuleId, rpcMsg.RpcId, rpcMsg.CurrentChunkNumber+1, rpcMsg.TotalChunks, location)
	srv.rpcDelayQueue.Store(rpcMsg.RpcId, rpcMsg.ExpirationTime)
	// For larger messages which get split into multiple chunks, cache them until all of them arrive
	if rpcMsg.TotalChunks > 1 {
		// Handle multiple chunks
		if !srv.handleChunks(rpcMsg) {
			return nil
		}
		if data, ok := srv.messageCache.Load(rpcMsg.RpcId); ok {
			rpcContent = data.([]byte)
		}
		// Remove rpcId from cache
		srv.messageCache.Delete(rpcMsg.RpcId)
		srv.currentChunkCache.Delete(rpcMsg.RpcId)
	}
	return &ipc.RpcRequestProto{
		RpcId:          rpcMsg.RpcId,
		ModuleId:       rpcMsg.ModuleId,
		ExpirationTime: rpcMsg.ExpirationTime,
		SystemId:       rpcMsg.SystemId,
		Location:       location,
		RpcContent:     rpcContent,
	}
}

// Transforms IPC RPC Response Message into a set of RPC API Messages and send them to Kafka.
func (srv *OnmsGrpcIPC) transformAndSendRPCMessage(msg *ipc.RpcResponseProto) {
	totalChunks := srv.getTotalChunks(msg.RpcContent)
	for chunk := int32(0); chunk < totalChunks; chunk++ {
		bufferSize := srv.getRemainingBufferSize(int32(len(msg.RpcContent)), chunk)
		offset := chunk * srv.getMaxBufferSize()
		data := msg.RpcContent[offset : offset+bufferSize]
		rpcMsg := &rpc.RpcMessageProto{
			RpcId:              msg.RpcId,
			ModuleId:           msg.ModuleId,
			SystemId:           msg.SystemId,
			RpcContent:         data,
			CurrentChunkNumber: chunk,
			TotalChunks:        totalChunks,
			TracingInfo:        msg.TracingInfo,
		}
		if bytes, err := proto.Marshal(rpcMsg); err != nil {
			srv.log.Errorf("cannot serialize RPC message: %v", err)
		} else {
			srv.log.Debugf("sending %s RPC response %s for location %s", msg.ModuleId, msg.RpcId, msg.Location)
			srv.sendToKafka(srv.getResponseTopic(), msg.RpcId, bytes)
		}
	}
}

// Processes the chunks of a given RPC API Message.
// Returns true when all the chunks have been processed.
func (srv *OnmsGrpcIPC) handleChunks(rpcMsg *rpc.RpcMessageProto) bool {
	data, _ := srv.currentChunkCache.LoadOrStore(rpcMsg.RpcId, int32(0))
	chunkNumber := data.(int32)
	if chunkNumber != rpcMsg.CurrentChunkNumber {
		srv.log.Warnf("expected chunk = %d but got chunk = %d, ignoring.", chunkNumber, rpcMsg.CurrentChunkNumber)
		return false
	}
	data, _ = srv.messageCache.LoadOrStore(rpcMsg.RpcId, make([]byte, 0))
	srv.messageCache.Store(rpcMsg.RpcId, append(data.([]byte), rpcMsg.RpcContent...))
	chunkNumber++
	srv.currentChunkCache.Store(rpcMsg.RpcId, chunkNumber)
	return rpcMsg.TotalChunks == chunkNumber
}

// Obtains the IPC RPC Handler for a given location or systemID when present.
func (srv *OnmsGrpcIPC) getRPCHandler(location string, systemID string) ipc.OpenNMSIpc_RpcStreamingServer {
	if systemID != "" {
		stream, _ := srv.rpcHandlerByMinionID.Load(systemID)
		if stream == nil {
			return nil
		}
		return stream.(ipc.OpenNMSIpc_RpcStreamingServer)
	}
	obj, _ := srv.rpcHandlerByLocation.Load(location)
	if obj == nil {
		return nil
	}
	handlerMap := obj.(*RoundRobinHandlerMap)
	return handlerMap.Get()
}

// Forwards the IPC RPC request to the appropriate stream handler.
func (srv *OnmsGrpcIPC) sendRequest(location string, rpcRequest *ipc.RpcRequestProto) {
	stream := srv.getRPCHandler(location, rpcRequest.SystemId)
	if stream == nil {
		srv.log.Warnf("no RPC handler found for location %s", location)
		return
	}
	if rpcRequest.SystemId == "" {
		srv.log.Debugf("sending gRPC request %s for location %s", rpcRequest.RpcId, location)
	} else {
		srv.log.Debugf("sending gRPC request %s to minion %s for location %s", rpcRequest.RpcId, rpcRequest.SystemId, location)
	}
	if err := stream.SendMsg(rpcRequest); err != nil {
		srv.log.Errorf("cannot send RPC request: %v", err)
	}
}

// Gets the RPC Request Topic for a given location
func (srv *OnmsGrpcIPC) getRequestTopicAtLocation(location string) string {
	return fmt.Sprintf(topicNameAtLocation, srv.config.OnmsInstanceID, location, rpcRequestTopicName)
}

// Gets the RPC Response Topic
func (srv *OnmsGrpcIPC) getResponseTopic() string {
	return fmt.Sprintf(topicNameWithoutLocation, srv.config.OnmsInstanceID, rpcResponseTopicName)
}

// Common/Helper Methods

func (srv *OnmsGrpcIPC) getMaxBufferSize() int32 {
	return int32(srv.config.MaxBufferSize)
}

func (srv *OnmsGrpcIPC) getTotalChunks(data []byte) int32 {
	if srv.config.MaxBufferSize == 0 {
		return int32(1)
	}
	chunks := int32(len(data) / srv.config.MaxBufferSize)
	if len(data)%srv.config.MaxBufferSize > 0 {
		chunks++
	}
	return chunks
}

func (srv *OnmsGrpcIPC) getRemainingBufferSize(messageSize, chunk int32) int32 {
	if srv.config.MaxBufferSize > 0 && messageSize > srv.getMaxBufferSize() {
		remaining := messageSize - chunk*srv.getMaxBufferSize()
		if remaining > srv.getMaxBufferSize() {
			return srv.getMaxBufferSize()
		}
		return remaining
	}
	return messageSize
}

func (srv *OnmsGrpcIPC) sendToKafka(topic string, key string, value []byte) {
	msg := &kafka.Message{
		TopicPartition: kafka.TopicPartition{Topic: &topic, Partition: kafka.PartitionAny},
		Key:            []byte(key),
		Value:          value,
	}
	if err := srv.producer.Produce(msg, nil); err != nil {
		srv.log.Errorf("cannot send message with key %s to topic %s: %v", key, topic, err)
	}
}
