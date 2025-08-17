package config

import (
	"log"
	"os"
	"time"

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
	setEngineDefaults(&config.Engine)

	return config, nil
}

func setEngineDefaults(engine *EngineConfig) {
	// Set default values based on comments in engine.go

	// MinTimerDuration default is 500 ms
	if engine.MinTimerDuration == 0 {
		engine.MinTimerDuration = 500 * time.Millisecond
	}

	// MaxTimerPayloadSizeInBytes default is 100 KB (100 * 1024 bytes)
	if engine.MaxTimerPayloadSizeInBytes == 0 {
		engine.MaxTimerPayloadSizeInBytes = 100 * 1024
	}

	// MaxiCallbackTimeoutSeconds default is 10 seconds
	if engine.MaxiCallbackTimeoutSeconds == 0 {
		engine.MaxiCallbackTimeoutSeconds = 10
	}

	// EngineShutdownTimeout default is 10 seconds
	if engine.EngineShutdownTimeout == 0 {
		engine.EngineShutdownTimeout = 10 * time.Second
	}

	// ShardEngineShutdownTimeout default is 2 seconds
	if engine.ShardEngineShutdownTimeout == 0 {
		engine.ShardEngineShutdownTimeout = 2 * time.Second
	}

	// Set defaults for nested configs
	setCallbackProcessorDefaults(&engine.CallbackProcessorConfig)
	setTimerQueueDefaults(&engine.TimerQueueConfig)
	setTimerBatchDeleterDefaults(&engine.TimerBatchDeleterConfig)
	setTimerBatchReaderDefaults(&engine.TimerBatchReaderConfig)
}

func setCallbackProcessorDefaults(config *CallbackProcessorConfig) {
	// Concurrency default is 2000
	if config.Concurrency == 0 {
		config.Concurrency = 2000
	}
}

func setTimerQueueDefaults(config *TimerQueueConfig) {
	// ExpectedQueueSize default is 500
	if config.ExpectedQueueSize == 0 {
		config.ExpectedQueueSize = 500
	}

	// MaxQueueSizeToUnload default is 1000
	if config.MaxQueueSizeToUnload == 0 {
		config.MaxQueueSizeToUnload = 1000
	}
}

func setTimerBatchDeleterDefaults(config *TimerBatchDeleterConfig) {
	// DeletingInterval default is 30 seconds
	if config.DeletingInterval == 0 {
		config.DeletingInterval = 30 * time.Second
	}

	// DeletingIntervalJitter default is 5 seconds
	if config.DeletingIntervalJitter == 0 {
		config.DeletingIntervalJitter = 5 * time.Second
	}

	// CommittingInterval default is 10 seconds
	if config.CommittingInterval == 0 {
		config.CommittingInterval = 10 * time.Second
	}

	// CommittingIntervalJitter default is 5 seconds
	if config.CommittingIntervalJitter == 0 {
		config.CommittingIntervalJitter = 5 * time.Second
	}

	// DeletingBatchLimitPerRequest default is 1000
	if config.DeletingBatchLimitPerRequest == 0 {
		config.DeletingBatchLimitPerRequest = 1000
	}
}

func setTimerBatchReaderDefaults(config *TimerBatchReaderConfig) {
	// QueueAvailableThresholdToLoad default is 0.5
	if config.QueueAvailableThresholdToLoad == 0 {
		config.QueueAvailableThresholdToLoad = 0.5
	}

	// MaxPreloadTimeDuration default is 1 minute
	if config.MaxPreloadTimeDuration == nil {
		duration := 1 * time.Minute
		config.MaxPreloadTimeDuration = &duration
	}

	// MaxLookAheadTimeDuration default is 1 minutes
	if config.MaxLookAheadTimeDuration == 0 {
		config.MaxLookAheadTimeDuration = 1 * time.Minute
	}

	// BatchReadLimitPerRequest default is 1000
	if config.BatchReadLimitPerRequest == 0 {
		config.BatchReadLimitPerRequest = 1000
	}
}
