package main

import (
	"testing"

	"github.com/confluentinc/confluent-kafka-go/kafka"
	"gotest.tools/v3/assert"
)

func TestPropertiesFlag(t *testing.T) {
	properties := PropertiesFlag{}
	properties.Set("a")
	properties.Set("b")
	assert.Equal(t, 2, len(properties))
	assert.Equal(t, "a, b", properties.String())
}

func TestUpdateKafkaConfig(t *testing.T) {
	properties := PropertiesFlag{
		"acks=1",
		"max.poll.records=50",
		"max.partition.fetch.bytes=1000000",
	}
	srv := &ServerConfig{}
	cfg := &kafka.ConfigMap{}
	err := srv.UpdateKafkaConfig(cfg, properties)
	assert.NilError(t, err)
	acks, ok := cfg.Get("acks", "0")
	assert.Assert(t, ok)
	assert.Equal(t, "1", acks)
}

func TestRoundRobinHandlerMap(t *testing.T) {
	handlerMap := new(RoundRobinHandlerMap)
	handlerMap.Set("001", &mockRPCStreamingServer{"H1"})
	handlerMap.Set("002", &mockRPCStreamingServer{"H2"})
	handlerMap.Set("003", &mockRPCStreamingServer{"H3"})
	// Verify Size
	assert.Equal(t, 3, len(handlerMap.handlerIDs))
	// Verify round-robin logic
	h := handlerMap.Get()
	assert.Equal(t, 1, handlerMap.current)
	assert.Equal(t, "H2", h.Context().Value(ID_FIELD))
	h = handlerMap.Get()
	assert.Equal(t, 2, handlerMap.current)
	assert.Equal(t, "H3", h.Context().Value(ID_FIELD))
	h = handlerMap.Get()
	assert.Equal(t, 0, handlerMap.current)
	assert.Equal(t, "H1", h.Context().Value(ID_FIELD))
	// Verify contains logic
	assert.Assert(t, handlerMap.Contains("001"))
	assert.Assert(t, !handlerMap.Contains("004"))
	// Verify update
	handlerMap.Set("003", &mockRPCStreamingServer{"H33"})
	assert.Equal(t, 3, len(handlerMap.handlerIDs))
	h = handlerMap.Find("003")
	assert.Equal(t, "H33", h.Context().Value(ID_FIELD))
}
