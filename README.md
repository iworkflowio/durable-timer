# durable-timer 🚧WIP🚧
A highly scalable, performant and distributed durable timer service

* [Requirement docs](./REQUIREMENTS.md)
* [API Design docs](./docs/design/api-design.md)
* [Database Design docss](./docs/design/database-design.md)
* [Repo layout](./docs/repo-layout.md)
* [Design decisoins](./DECISION_LOG.md)

## Use cases
* Backoff retry(distributed, **NOT** local)
* Durable timer(reminder notification, scheduled actions, etc)


## Supported databases
* [x] Cassandra
* [x] ScyllaDB
* [x] MongoDB
* [x] DynamoDB
* [x] MySQL
* [x] PostgreSQL
