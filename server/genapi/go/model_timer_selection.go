/*
 * Distributed Durable Timer Service API
 *
 * A distributed, durable timer service that can schedule and execute HTTP webhook callbacks  at specified times with high reliability and scalability.  ## Features - Create one-time timers with custom payloads - Durable persistence across service restarts - At-least-once delivery semantics - Configurable retry policies and timeouts - Timer modification and cancellation - Callback response can update timer schedule - Namespace-based timer uniqueness
 *
 * API version: 1.0.0
 * Generated by: OpenAPI Generator (https://openapi-generator.tech)
 */

package genapi

type TimerSelection struct {

	// Namespace identifier for the timer. It is for timer ID uniqueness. Also used for scalability design(tied to the number of shards). Must be one of the namespaces configured in the system.
	Namespace string `json:"namespace"`

	// Unique identifier for the timer within the namespace
	TimerId string `json:"timerId"`
}
