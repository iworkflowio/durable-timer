package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/iworkflowio/durable-timer/config"
	"github.com/iworkflowio/durable-timer/databases"
	"github.com/iworkflowio/durable-timer/engine/backoff"
	"github.com/iworkflowio/durable-timer/log"
	"github.com/iworkflowio/durable-timer/log/tag"
)

type callbackProcessorImpl struct {
	config                     *config.Config
	logger                     log.Logger
	processingChannel          <-chan *databases.DbTimer
	processingCompletedChannel chan<- *databases.DbTimer
	timerStore                 databases.TimerStore

	// Worker pool management
	ctx        context.Context
	cancel     context.CancelFunc
	workerWg   sync.WaitGroup
	httpClient *http.Client
}

func newCallbackProcessorImpl(
	config *config.Config, logger log.Logger,
	processingChannel <-chan *databases.DbTimer,
	processingCompletedChannel chan<- *databases.DbTimer,
) (CallbackProcessor, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// Create HTTP client with reasonable defaults
	// TODO: make this configurable
	httpClient := &http.Client{
		Timeout: time.Duration(config.Engine.MaxCallbackTimeoutSeconds) * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        config.Engine.CallbackProcessorConfig.Concurrency,
			MaxIdleConnsPerHost: config.Engine.CallbackProcessorConfig.Concurrency,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	return &callbackProcessorImpl{
		config:                     config,
		logger:                     logger,
		processingChannel:          processingChannel,
		processingCompletedChannel: processingCompletedChannel,
		ctx:                        ctx,
		cancel:                     cancel,
		httpClient:                 httpClient,
	}, nil
}

// Start implements CallbackProcessor.
func (c *callbackProcessorImpl) Start() error {
	concurrency := c.config.Engine.CallbackProcessorConfig.Concurrency
	c.logger.Info("Starting callback processor, concurrency ", tag.Value(concurrency))

	// Start worker goroutines
	for i := 0; i < concurrency; i++ {
		c.workerWg.Add(1)
		go c.worker(i)
	}

	return nil
}

// Close implements CallbackProcessor.
func (c *callbackProcessorImpl) Close() error {
	c.logger.Info("Shutting down callback processor")

	// Cancel context to signal workers to stop
	c.cancel()

	// Wait for all workers to finish with timeout
	done := make(chan struct{})
	go func() {
		c.workerWg.Wait()
		close(done)
	}()

	select {
	case <-done:
		c.logger.Info("Callback processor shutdown completed")
	case <-time.After(c.config.Engine.EngineShutdownTimeout):
		c.logger.Warn("Callback processor shutdown timed out")
	}

	return nil
}

// worker processes timers from the processing channel
func (c *callbackProcessorImpl) worker(workerID int) {
	defer c.workerWg.Done()

	c.logger.Debug("Callback worker started ", tag.Value(workerID))

	for {
		select {
		case <-c.ctx.Done():
			c.logger.Debug("Callback worker stopping")
			return
		case timer, ok := <-c.processingChannel:
			if !ok {
				c.logger.Debug("Processing channel closed, worker stopping")
				return
			}

			c.processTimer(c.logger, timer)
		}
	}
}

// processTimer handles a single timer callback
func (c *callbackProcessorImpl) processTimer(logger log.Logger, timer *databases.DbTimer) {
	logger = logger.WithTags(
		tag.TimerId(timer.Id),
		tag.Namespace(timer.Namespace),
		tag.Value(timer.CallbackUrl),
	)

	logger.Info("Processing timer callback")

	// Update timer with execution time and attempt count
	timer.ExecutedAt = time.Now()
	timer.Attempts++

	// Execute the callback with retry logic.
	// There are four cases:
	// 1. (happy case)success=true and nextExecuteAt is zero, only send the timer to the processing completed channel
	// 2. (reschedule case)success=true, but nextExecuteAt is not zero (must be after now), update the nextExecuteAt and reset attempts
	// 3. (failure and retry)success=false, but nextExecuteAt is not zero, update the nextExecuteAt and increase attempts
	// 4. (failure and max out of retries) success=false, but nextExecuteAt is zero(must be after now), update the nextExecuteAt to infinity and increase attempts
	success, nextExecuteAt := c.executeCallback(logger, timer)

	if success {
		if nextExecuteAt.IsZero() {
			// case 1, noop here
		} else {
			// case 2
			// TODO: use UpdadateExecuteAt API to update the nextExecuteAt
			err := c.timerStore.UpdateTimerNoLock(c.ctx, timer.ShardId, timer.Namespace, &databases.UpdateDbTimerRequest{
				TimerId:   timer.Id,
				ExecuteAt: nextExecuteAt,
				// TODO Attempts:  0,
				// lastExecutedAt: time.Now(),
			})
			if err != nil {
				panic("TODO: use local backoff retry to never reach here")
			}
		}
	} else {

		if !nextExecuteAt.IsZero() {
			// case 4
			// TODO: configurable
			infiniteTime := time.Now().Add(time.Hour * 24 * 365)
			nextExecuteAt = infiniteTime
		}
		err := c.timerStore.UpdateTimerNoLock(c.ctx, timer.ShardId, timer.Namespace, &databases.UpdateDbTimerRequest{
			TimerId:   timer.Id,
			ExecuteAt: nextExecuteAt,
			// TODO Attempts:  timer.Attempts +1,
			// lastExecutedAt: time.Now(),
		})
		if err != nil {
			panic("TODO: use local backoff retry to never reach here")
		}
	}

	c.processingCompletedChannel <- timer
	// TODO: handle the case where the channel is closed
	// maybe making sure always closing the channels lastly.
}

// executeCallback executes the HTTP callback with retry logic
func (c *callbackProcessorImpl) executeCallback(logger log.Logger, timer *databases.DbTimer) (success bool, nextExecuteAt time.Time) {
	// Create retry policy from timer configuration
	retryPolicy := c.createRetryPolicy(timer)

	// Create retrier
	retrier := backoff.NewRetrier(retryPolicy, nil)

	var lastErr error
	for {
		// Execute the HTTP callback
		success, nextExecuteAt, err := c.makeHTTPCallback(timer)
		if err == nil {
			if success {
				return true, time.Time{}
			}
			// Callback returned success=false, check if we should reschedule
			if !nextExecuteAt.IsZero() {
				return false, nextExecuteAt
			}
			// Callback failed but we can retry
			lastErr = fmt.Errorf("callback returned ok=false")
		} else {
			lastErr = err
			logger.Warn("HTTP callback failed",
				tag.Error(err))
		}

		// Check if error is retryable
		if !c.isRetryableError(err) {
			logger.Warn("Non-retryable error encountered",
				tag.Error(err))
			return false, time.Time{}
		}

		// Check if we should retry
		nextBackoff := retrier.NextBackOff()
		if nextBackoff < 0 {
			logger.Warn("Max retries exceeded or retry policy expired",
				tag.Error(lastErr))
			return false, time.Time{}
		}

		// Sleep before retry
		time.Sleep(nextBackoff)
	}
}

// makeHTTPCallback makes the actual HTTP request to the callback URL
func (c *callbackProcessorImpl) makeHTTPCallback(timer *databases.DbTimer) (success bool, nextExecuteAt time.Time, err error) {
	// Prepare the request payload
	payload := map[string]interface{}{
		"timerId":   timer.Id,
		"namespace": timer.Namespace,
		"executeAt": timer.ExecuteAt,
		"payload":   timer.Payload,
		"attempts":  timer.Attempts,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return false, time.Time{}, fmt.Errorf("failed to marshal payload: %w", err)
	}

	// Create HTTP request with timeout
	ctx, cancel := context.WithTimeout(c.ctx, time.Duration(timer.CallbackTimeoutSeconds)*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", timer.CallbackUrl,
		strings.NewReader(string(jsonData)))
	if err != nil {
		return false, time.Time{}, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "durable-timer/1.0")

	// Make the HTTP request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, time.Time{}, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	// Parse response based on status code
	switch resp.StatusCode {
	case http.StatusOK:
		// Parse callback response
		var callbackResp struct {
			Ok            bool      `json:"ok"`
			NextExecuteAt time.Time `json:"nextExecuteAt,omitempty"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&callbackResp); err != nil {
			return false, time.Time{}, fmt.Errorf("failed to parse callback response: %w", err)
		}

		if callbackResp.Ok {
			return true, time.Time{}, nil
		}

		// Timer wants to be rescheduled
		return false, callbackResp.NextExecuteAt, nil

	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		// 4xx errors are not retryable
		return false, time.Time{}, fmt.Errorf("client error %d: %s", resp.StatusCode, resp.Status)

	default:
		// 5xx and other errors are retryable
		return false, time.Time{}, fmt.Errorf("server error %d: %s", resp.StatusCode, resp.Status)
	}
}

// createRetryPolicy creates a retry policy from the timer's retry configuration
func (c *callbackProcessorImpl) createRetryPolicy(timer *databases.DbTimer) backoff.RetryPolicy {
	// Default retry policy if none specified
	defaultPolicy := backoff.NewExponentialRetryPolicy(30 * time.Second)
	defaultPolicy.SetMaximumAttempts(3)

	if timer.RetryPolicy == nil {
		return defaultPolicy
	}

	// Parse retry policy from timer
	// Note: This is a simplified implementation. In a real system, you'd want
	// to properly deserialize the RetryPolicy from the timer's RetryPolicy field
	// and convert it to the backoff.RetryPolicy interface

	// For now, return the default policy
	return defaultPolicy
}

// isRetryableError determines if an error should trigger a retry
func (c *callbackProcessorImpl) isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Network errors, timeouts, and 5xx errors are retryable
	// 4xx errors are generally not retryable
	return true
}
