package sinks

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io/ioutil"
	"math/rand"
	"time"

	"github.com/Shopify/sarama"
	"github.com/opsgenie/kubernetes-event-exporter/pkg/kube"
	"github.com/rs/zerolog/log"
)

// KafkaConfig is the Kafka producer configuration
type KafkaConfig struct {
	Topic        	 string                 `yaml:"topic"`
	Brokers      	 []string               `yaml:"brokers"`
	Layout       	 map[string]interface{} `yaml:"layout"`
	ClientId     	 string                 `yaml:"clientId"`
	Version      	 string                 `yaml:"version"`
	MaxBytesSize 	 int                    `yaml:"maxBytesSize" default:"1000000"`
	Timeout      	 int32                  `yaml:"timeout" default:"10"`
	CompressionCodec string					`yaml:"compressionCodec" default:"none"`
	KeepAlive        int32					`yaml:"keepAlive" default:"0"`
	TLS     struct {
		Enable             bool   `yaml:"enable"`
		CaFile             string `yaml:"caFile"`
		CertFile           string `yaml:"certFile"`
		KeyFile            string `yaml:"keyFile"`
		InsecureSkipVerify bool   `yaml:"insecureSkipVerify"`
	} `yaml:"tls"`
	SASL struct {
		Enable    bool   `yaml:"enable"`
		Username  string `yaml:"username"`
		Password  string `yaml:"password"`
	} `yaml:"sasl"`
}

// KafkaSink is a sink that sends events to a Kafka topic
type KafkaSink struct {
	producer sarama.SyncProducer
	cfg      *KafkaConfig
}

var CompressionCodecs = map[string]sarama.CompressionCodec{
	"none": sarama.CompressionNone,
	"snappy": sarama.CompressionSnappy,
	"gzip": sarama.CompressionGZIP,
	"lz4": sarama.CompressionLZ4,
	"zstd": sarama.CompressionZSTD,
}

func NewKafkaSink(cfg *KafkaConfig) (Sink, error) {
	producer, err := createSaramaProducer(cfg)
	if err != nil {
		return nil, err
	}

	log.Info().Msgf("kafka: Producer initialized for topic: %s, brokers: %s", cfg.Topic, cfg.Brokers)

	return &KafkaSink{
		producer: producer,
		cfg:      cfg,
	}, nil
}

// Send an event to Kafka synchronously
func (k *KafkaSink) Send(ctx context.Context, ev *kube.EnhancedEvent) error {
	var toSend []byte

	if k.cfg.Layout != nil {
		res, err := convertLayoutTemplate(k.cfg.Layout, ev)
		if err != nil {
			return err
		}

		toSend, err = json.Marshal(res)
		if err != nil {
			return err
		}
	} else {
		toSend = ev.ToJSON()
	}

	_, _, err := k.producer.SendMessage(&sarama.ProducerMessage{
		Topic: k.cfg.Topic,
		Key:   sarama.StringEncoder(string(ev.UID)),
		Value: sarama.ByteEncoder(toSend),
	})

	return err
}

// Close the Kafka producer
func (k *KafkaSink) Close() {
	log.Info().Msgf("kafka: Closing producer...")

	if err := k.producer.Close(); err != nil {
		log.Error().Err(err).Msg("Failed to shut down the Kafka producer cleanly")
	} else {
		log.Info().Msg("kafka: Closed producer")
	}
}

func createSaramaProducer(cfg *KafkaConfig) (sarama.SyncProducer, error) {
	// Default Sarama config
	saramaConfig := sarama.NewConfig()
	saramaConfig.Version = sarama.MaxVersion
	saramaConfig.Metadata.Full = true
	saramaConfig.ClientID = cfg.ClientId

	// Necessary for SyncProducer
	saramaConfig.Producer.Return.Successes = true
	saramaConfig.Producer.Return.Errors = true
	if _, ok := CompressionCodecs[cfg.CompressionCodec]; ok {
		saramaConfig.Producer.Compression = CompressionCodecs[cfg.CompressionCodec]
	}
	saramaConfig.Producer.MaxMessageBytes = cfg.MaxBytesSize
	saramaConfig.Producer.Timeout = time.Duration(rand.Int31n(cfg.Timeout)) * time.Second

	// Net Config
	saramaConfig.Net.KeepAlive = time.Duration(rand.Int31n(cfg.KeepAlive)) * time.Second

	// TLS Client auth override
	if cfg.TLS.Enable {

		caCert, err := ioutil.ReadFile(cfg.TLS.CaFile)
		if err != nil {
			return nil, err
		}

		caCertPool := x509.NewCertPool()
		caCertPool.AppendCertsFromPEM(caCert)

		saramaConfig.Net.TLS.Enable = true
		saramaConfig.Net.TLS.Config = &tls.Config{
			RootCAs:            caCertPool,
			InsecureSkipVerify: cfg.TLS.InsecureSkipVerify,
		}

		if cfg.TLS.CertFile != "" && cfg.TLS.KeyFile != "" {
			cert, err := tls.LoadX509KeyPair(cfg.TLS.CertFile, cfg.TLS.KeyFile)
			if err != nil {
				return nil, err
			}

			saramaConfig.Net.TLS.Config.Certificates = []tls.Certificate{cert}
		}
	}

	// SASL Client auth
	if cfg.SASL.Enable {
		saramaConfig.Net.SASL.Enable = true
		saramaConfig.Net.SASL.User = cfg.SASL.Username
		saramaConfig.Net.SASL.Password = cfg.SASL.Password
	}

	// TODO: Find a generic way to override all other configs

	// Build producer
	producer, err := sarama.NewSyncProducer(cfg.Brokers, saramaConfig)
	if err != nil {
		return nil, err
	}

	return producer, nil
}
