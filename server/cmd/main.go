/*
 * Distributed Durable Timer Service API
 *
 * A distributed, durable timer service that can schedule and execute HTTP webhook callbacks  at specified times with high reliability and scalability.  ## Features - Create one-time timers with custom payloads - Durable persistence across service restarts - At-least-once delivery semantics - Configurable retry policies and timeouts - Timer modification and cancellation - Callback response can update timer schedule
 *
 * API version: 1.0.0
 * Generated by: OpenAPI Generator (https://openapi-generator.tech)
 */

package main

import (
	"log"
)

func main() {
	routes := ApiHandleFunctions{}

	log.Printf("Server started")

	router := NewRouter(routes)

	log.Fatal(router.Run(":8080"))
}
