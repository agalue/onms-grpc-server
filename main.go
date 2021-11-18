package main

import (
	"flag"
	"os"
	"os/signal"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func main() {
	var logLevel string
	cfg := new(ServerConfig)
	flag.IntVar(&cfg.GrpcPort, "port", defaultGrpcPort, "gRPC Server Listener Port")
	flag.IntVar(&cfg.HTTPPort, "http-port", defaultHTTPPort, "HTTP Server Listener Port (Prometheus Metrics)")
	flag.StringVar(&cfg.OnmsInstanceID, "instance-id", defaultInstanceID, "OpenNMS Instance ID")
	flag.StringVar(&cfg.KafkaBootstrap, "bootstrap", "localhost:9092", "Kafka Bootstrap Server")
	flag.Var(&cfg.KafkaProducerProperties, "producer-cfg", "Kafka Producer configuration entry (can be used multiple times)\nfor instance: acks=1")
	flag.Var(&cfg.KafkaConsumerProperties, "consumer-cfg", "Kafka Consumer configuration entry (can be used multiple times)\nfor instance: acks=1")
	flag.IntVar(&cfg.MaxBufferSize, "max-buffer-size", defaultMaxByfferSize, "Maximum Buffer Size for RPC/Sink API Messages")
	flag.BoolVar(&cfg.TLSEnabled, "tls-enabled", false, "Enable TLS for the gRPC Server")
	flag.StringVar(&cfg.TLSServerCertFile, "tls-server-cert", "", "Path to the Server Certificate file")
	flag.StringVar(&cfg.TLSServerKeyFile, "tls-server-key", "", "Path to the Server Key file")
	flag.StringVar(&cfg.TLSClientCACertFile, "tls-client-ca-cert", "", "Path to the Client CA Certificate file (enables mTLS)")
	flag.StringVar(&logLevel, "log-level", "info", "Log Level")
	flag.Parse()

	level := zap.NewAtomicLevel()
	switch strings.ToLower(logLevel) {
	case "debug":
		level.SetLevel(zap.DebugLevel)
	case "info":
		level.SetLevel(zap.InfoLevel)
	case "warn":
		level.SetLevel(zap.WarnLevel)
	case "error":
		level.SetLevel(zap.ErrorLevel)
	}
	config := zap.Config{
		Level:             level,
		Development:       false,
		DisableStacktrace: true,
		Encoding:          "console",
		EncoderConfig: zapcore.EncoderConfig{
			TimeKey:        "ts",
			LevelKey:       "level",
			NameKey:        "logger",
			MessageKey:     "msg",
			CallerKey:      "caller",
			LineEnding:     zapcore.DefaultLineEnding,
			EncodeLevel:    zapcore.CapitalColorLevelEncoder,
			EncodeTime:     zapcore.ISO8601TimeEncoder,
			EncodeDuration: zapcore.StringDurationEncoder,
			EncodeCaller:   zapcore.ShortCallerEncoder,
		},
		OutputPaths:      []string{"stdout"},
		ErrorOutputPaths: []string{"stderr"},
	}

	logger, err := config.Build()
	if err != nil {
		panic(err)
	}

	srv := &OnmsGrpcServer{Config: cfg, Logger: logger}
	if err := srv.Start(); err != nil {
		srv.Logger.Fatal(err.Error())
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	<-stop
	srv.Stop()
}
