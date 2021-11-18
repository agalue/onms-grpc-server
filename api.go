package main

import (
	"strings"
	"sync"

	"github.com/agalue/onms-grpc-server/protobuf/ipc"
	"github.com/confluentinc/confluent-kafka-go/kafka"
)

const (
	topicNameAtLocation          = "%s.%s.%s"
	topicNameWithoutLocation     = "%s.%s"
	sinkTopicNameWithoutLocation = "%s.%s.%s"
	sinkModulePrefix             = "Sink"
	rpcRequestTopicName          = "rpc-request"
	rpcResponseTopicName         = "rpc-response"
	defaultGrpcPort              = 8990
	defaultHTTPPort              = 2112
	defaultMaxByfferSize         = 921600
	defaultInstanceID            = "OpenNMS"
)

// KafkaConsumer represents an generic interface with the relevant methods from kafka.Consumer.
// This allows to use a mock implementation for testing purposes.
type KafkaConsumer interface {
	Subscribe(topic string, rebalanceCb kafka.RebalanceCb) error
	Poll(timeoutMs int) (event kafka.Event)
	CommitMessage(m *kafka.Message) ([]kafka.TopicPartition, error)
	Unsubscribe() error
	Close() error
}

// KafkaProducer represents an generic interface with the relevant methods from kafka.Producer.
// This allows to use a mock implementation for testing purposes.
type KafkaProducer interface {
	Produce(msg *kafka.Message, deliveryChan chan kafka.Event) error
	Events() chan kafka.Event
	Close()
}

// ServerConfig represents a server configuration
type ServerConfig struct {
	GrpcPort                int
	HTTPPort                int
	KafkaBootstrap          string
	KafkaProducerProperties PropertiesFlag
	KafkaConsumerProperties PropertiesFlag
	OnmsInstanceID          string
	MaxBufferSize           int
	TLSEnabled              bool
	TLSServerCertFile       string
	TLSServerKeyFile        string
	TLSClientCACertFile     string
}

// Updates the Kafka configuration map with a list of property flags
func (srv *ServerConfig) UpdateKafkaConfig(cfg *kafka.ConfigMap, properties PropertiesFlag) error {
	for _, kv := range properties {
		array := strings.Split(kv, "=")
		if err := cfg.SetKey(array[0], array[1]); err != nil {
			return err
		}
	}
	return nil
}

// PropertiesFlag represents an array of string flags
type PropertiesFlag []string

func (p *PropertiesFlag) String() string {
	return strings.Join(*p, ", ")
}

// Set stores a string flag in the array
func (p *PropertiesFlag) Set(value string) error {
	*p = append(*p, value)
	return nil
}

// RoundRobinHandlerMap is an RPC Handler Round Robin map
type RoundRobinHandlerMap struct {
	handlerIDs []string
	handlerMap *sync.Map
	current    int
}

// Set adds a new handler to the round-robin map or updates it if exist
func (h *RoundRobinHandlerMap) Set(id string, handler ipc.OpenNMSIpc_RpcStreamingServer) {
	if h.handlerMap == nil {
		h.handlerMap = new(sync.Map)
	}
	if _, ok := h.handlerMap.Load(id); !ok {
		h.handlerIDs = append(h.handlerIDs, id)
	}
	h.handlerMap.Store(id, handler)
}

// Get obtains the next handler in a round-robin basis
func (h *RoundRobinHandlerMap) Get() ipc.OpenNMSIpc_RpcStreamingServer {
	if h.handlerMap == nil {
		return nil
	}
	h.current++
	if h.current == len(h.handlerIDs) {
		h.current = 0
	}
	if handler, ok := h.handlerMap.Load(h.handlerIDs[h.current]); ok {
		if handler != nil {
			return handler.(ipc.OpenNMSIpc_RpcStreamingServer)
		}
	}
	return nil
}

// Find returns a given handler by its ID
func (h *RoundRobinHandlerMap) Find(id string) ipc.OpenNMSIpc_RpcStreamingServer {
	if h.handlerMap == nil {
		return nil
	}
	if handler, ok := h.handlerMap.Load(id); ok {
		if handler != nil {
			return handler.(ipc.OpenNMSIpc_RpcStreamingServer)
		}
	}
	return nil
}

// Contains returns true if the ID is present in the round-robin map
func (h *RoundRobinHandlerMap) Contains(id string) bool {
	if h.handlerMap == nil {
		return false
	}
	_, ok := h.handlerMap.Load(id)
	return ok
}
