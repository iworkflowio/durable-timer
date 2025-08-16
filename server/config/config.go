package config

import (
	"log"
	"os"

	"gopkg.in/yaml.v2"
)

type Config struct {
	Engine EngineConfig
}

func LoadConfigWithDefaults(configPath string) (*Config, error) {
	log.Printf("Loading configFile=%v\n", configPath)

	config := &Config{}

	file, err := os.Open(configPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	d := yaml.NewDecoder(file)

	if err := d.Decode(&config); err != nil {
		return nil, err
	}

	// set default values if not set
	

	return config, nil
}
