package config

import (
	"fmt"
	"os"
	"strings"
)

type Config struct {
	HTTPAddr          string
	Brokers           []string
	SchemaRegistryURL string
	GeneratorEnabled  bool
}

func Load() (Config, error) {
	brokers, registry, err := LoadRegistry()
	if err != nil {
		return Config{}, err
	}
	addr, err := required("HTTP_ADDR")
	if err != nil {
		return Config{}, err
	}
	rawGen, err := required("GENERATOR_ENABLED")
	if err != nil {
		return Config{}, err
	}
	var enabled bool
	switch rawGen {
	case "true":
		enabled = true
	case "false":
		enabled = false
	default:
		return Config{}, fmt.Errorf("GENERATOR_ENABLED must be true or false")
	}
	return Config{
		HTTPAddr:          addr,
		Brokers:           brokers,
		SchemaRegistryURL: registry,
		GeneratorEnabled:  enabled,
	}, nil
}

func LoadRegistry() ([]string, string, error) {
	rawBrokers, err := required("KAFKA_BROKERS")
	if err != nil {
		return nil, "", err
	}
	registry, err := required("SCHEMA_REGISTRY_URL")
	if err != nil {
		return nil, "", err
	}
	brokers := splitBrokers(rawBrokers)
	if len(brokers) == 0 {
		return nil, "", fmt.Errorf("KAFKA_BROKERS is required")
	}
	return brokers, registry, nil
}

func required(key string) (string, error) {
	value := os.Getenv(key)
	if value == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return value, nil
}

func splitBrokers(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
